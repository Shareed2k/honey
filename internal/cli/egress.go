package cli

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/hostapi"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/sshclient"
	"github.com/shareed2k/honey/internal/tun"
)

var (
	flagEgressPort      int      // default 1080; 0 = random port
	flagEgressBind      string   // default "127.0.0.1"
	flagEgressTun       bool     // enable TUN mode (requires root + tun2proxy)
	flagEgressAutoProxy bool     // auto-configure system SOCKS5 proxy
	flagEgressBypass    []string // extra IPs/CIDRs to bypass the TUN tunnel
	flagEgressNets      []string // route only these CIDRs (complement becomes --bypass)
	flagEgressAutoNets  bool     // discover remote subnets via SSH and use as --nets
	flagEgressPoolSize  int      // number of parallel SSH connections per host
)

var egressCmd = &cobra.Command{
	Use:   "egress <host>",
	Short: "Route traffic through a honey host via SOCKS5 (VPN-like exit)",
	Long: `Establishes a SOCKS5 proxy over SSH to the named host.
All TCP connections routed through it will exit from that host.

With --tun, all system traffic is transparently routed (requires root and tun2proxy).
Use --bypass <CIDR> to exclude local subnets (e.g. local Docker ranges) from the tunnel.
Press Ctrl+C to stop.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runEgress,
}

func init() {
	rootCmd.AddCommand(egressCmd)
	egressCmd.Flags().IntVar(&flagEgressPort, "port", 1080, "Local SOCKS5 listen port (0 = random)")
	egressCmd.Flags().StringVar(&flagEgressBind, "bind", "127.0.0.1", "Local bind address")
	egressCmd.Flags().BoolVar(&flagEgressTun, "tun", false, "Enable TUN mode: transparent VPN via tun2proxy (requires root)")
	egressCmd.Flags().BoolVar(&flagEgressAutoProxy, "auto-proxy", false, "Auto-configure system SOCKS5 proxy settings (macOS)")
	egressCmd.Flags().StringArrayVar(&flagEgressBypass, "bypass", nil, "IP or CIDR to bypass the TUN tunnel (repeatable; loopback/link-local auto-excluded)")
	egressCmd.Flags().StringArrayVar(&flagEgressNets, "nets", nil, "Route only these CIDRs through the tunnel (default: all traffic). Requires --tun. Repeatable.")
	egressCmd.Flags().BoolVar(&flagEgressAutoNets, "auto-nets", false, "Discover routable subnets on the remote host via SSH and route only those (requires --tun)")
	egressCmd.Flags().IntVar(&flagEgressPoolSize, "pool-size", 1, "Parallel SSH connections per host; >1 increases throughput and reconnects on drop")
	egressCmd.Flags().StringVar(&flagSSHUser, "ssh-user", "", "SSH user override")
	egressCmd.Flags().StringVar(&flagProviders, "provider", "", "Comma-separated: gcp,aws,k8s,consul,proxmox,truenas,docker,local (default: all)")
	egressCmd.Flags().StringVar(&flagBackends, "backends", "", "Comma-separated backend filter")
}

func runEgress(cmd *cobra.Command, args []string) error {
	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if net.ParseIP(flagEgressBind) == nil && !isValidHostname(flagEgressBind) {
		return fmt.Errorf("invalid --bind value %q: must be an IP address or hostname", flagEgressBind)
	}
	if (flagEgressAutoNets || len(flagEgressNets) > 0) && !flagEgressTun {
		return fmt.Errorf("--auto-nets and --nets require --tun")
	}
	if flagEgressTun && os.Getuid() != 0 {
		return fmt.Errorf("--tun requires root, re-run with sudo")
	}

	sshUser := flagSSHUser
	if sshUser == "" && resolvedCfg != nil {
		sshUser = resolvedCfg.Defaults.SSHUser
	}

	var wclients []sshclient.WeightedClient
	var peerNames []string
	var peerIPs []string
	var discoveredNets []string

	for _, arg := range args {
		hostArg, weight := parseEgressArg(arg)
		fmt.Printf("Resolving host: %s\n", hostArg)
		out, err := hostapi.SearchHosts(ctx, &hostapi.SearchHostsInput{
			Name:       hostArg,
			ConfigPath: resolvedCfgPath,
			SSHUser:    flagSSHUser,
			Providers:  flagProviders,
			Backends:   flagBackends,
		})
		if err != nil {
			return fmt.Errorf("resolve %q: %w", hostArg, err)
		}
		if len(out.Records) == 0 {
			return fmt.Errorf("host %q not found in inventory", hostArg)
		}
		rec := out.Records[0]
		ip := hosts.PrimaryIPTrimmed(rec)
		if ip == "" {
			return fmt.Errorf("host %q has no IP address", rec.Name)
		}
		zap.L().Debug("resolved host", zap.String("name", rec.Name), zap.String("ip", ip))
		sshPort := 0
		if p, ok := hosts.MetaSSHPort(&rec); ok {
			sshPort = p
		}
		identity := ""
		if id, ok := hosts.MetaSSHIdentityFile(&rec); ok {
			identity = id
		}
		fmt.Printf("Connecting to %s (%s) via SSH...\n", rec.Name, ip)
		honeyClient, err := sshclient.DialHoneyClient(sshUser, ip, sshPort, identity)
		if err != nil {
			return fmt.Errorf("ssh connect to %s: %w", rec.Name, err)
		}
		zap.L().Debug("ssh connected", zap.String("host", rec.Name), zap.String("ip", ip))
		// Run auto-nets on first host using this connection before pool takes over.
		if flagEgressAutoNets && len(discoveredNets) == 0 {
			discoveredNets = tun.QueryRemoteNets(honeyClient.LeafSSH())
			zap.L().Debug("auto-nets discovered", zap.Strings("nets", discoveredNets))
		}
		_ = honeyClient.Close()

		capturedUser, capturedIP, capturedPort, capturedIdentity := sshUser, ip, sshPort, identity
		dialFn := func() (*sshclient.HoneyClient, error) {
			return sshclient.DialHoneyClient(capturedUser, capturedIP, capturedPort, capturedIdentity)
		}
		pool, poolErr := sshclient.NewSSHPool(ctx, flagEgressPoolSize, dialFn)
		if poolErr != nil {
			return fmt.Errorf("pool for %s: %w", rec.Name, poolErr)
		}
		zap.L().Debug("ssh pool created", zap.String("host", rec.Name), zap.Int("size", flagEgressPoolSize))
		defer pool.Close()
		wclients = append(wclients, sshclient.WeightedClient{Client: pool, Weight: weight})
		peerNames = append(peerNames, rec.Name)
		peerIPs = append(peerIPs, ip)
	}

	nets := flagEgressNets
	if flagEgressAutoNets && len(discoveredNets) > 0 {
		fmt.Printf("Auto-nets discovered: %s\n", strings.Join(discoveredNets, ", "))
		nets = append(nets, discoveredNets...)
	}

	socksHost, socksPort, stop, err := sshclient.StartDynamicForwardMulti(ctx, wclients, flagEgressBind, flagEgressPort)
	if err != nil {
		return fmt.Errorf("start SOCKS5 proxy: %w", err)
	}
	defer stop()
	zap.L().Debug("socks5 proxy started", zap.String("addr", net.JoinHostPort(socksHost, strconv.Itoa(socksPort))))

	if flagEgressTun {
		zap.L().Debug("starting tun mode", zap.Strings("nets", nets), zap.Strings("ssh_ips", peerIPs))
		return tun.Run(ctx, tun.Config{
			SOCKSHost:     socksHost,
			SOCKSPort:     socksPort,
			HostName:      strings.Join(peerNames, ", "),
			SSHIPs:        peerIPs,
			ExtraBypasses: flagEgressBypass,
			Nets:          nets,
		})
	}

	printEgressInstructions(peerNames, socksHost, socksPort)
	if flagEgressAutoProxy {
		if cleanup, proxyErr := configureSystemSOCKSProxy(socksHost, socksPort); proxyErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: auto-proxy failed: %v\n", proxyErr)
		} else {
			defer cleanup()
			fmt.Println("System SOCKS5 proxy configured.")
		}
	}

	<-ctx.Done()
	zap.L().Debug("egress shutting down")
	fmt.Println("\nShutting down egress proxy...")
	return nil
}

func printEgressInstructions(hostNames []string, socksHost string, socksPort int) {
	addr := net.JoinHostPort(socksHost, strconv.Itoa(socksPort))
	label := "exit node"
	if len(hostNames) > 1 {
		label = "exit nodes (weighted round-robin)"
	}
	fmt.Printf("\nSOCKS5 proxy running at %s  (%s: %s)\n\n", addr, label, strings.Join(hostNames, ", "))
	fmt.Println("Configure your client:")
	fmt.Printf("  Browser:   SOCKS5 host=%s port=%d (enable 'Proxy DNS when using SOCKS5')\n", socksHost, socksPort)
	fmt.Printf("  curl:      curl --socks5-hostname %s <url>\n", addr)
	fmt.Println()
	// For the --tun hint, use first host name only
	firstName := ""
	if len(hostNames) > 0 {
		firstName = hostNames[0]
	}
	fmt.Printf("  For full transparent VPN:  honey egress %s --tun\n", firstName)
	fmt.Println()
	switch runtime.GOOS {
	case "darwin":
		fmt.Printf("  macOS (manual):  networksetup -setsocksfirewallproxy Wi-Fi %s %d\n", socksHost, socksPort)
		fmt.Printf("  macOS (undo):    networksetup -setsocksfirewallproxystate Wi-Fi off\n")
		fmt.Println()
		fmt.Printf("  Or use: honey egress %s --auto-proxy\n", firstName)
	case "linux":
		fmt.Printf("  Linux env:  export ALL_PROXY=socks5h://%s\n", addr)
	}
	fmt.Println("\nPress Ctrl+C to stop.")
}

// parseEgressArg splits "host" or "host:weight" into name and weight (default 1).
func parseEgressArg(arg string) (host string, weight int) {
	if i := strings.LastIndex(arg, ":"); i > 0 {
		if w, err := strconv.Atoi(arg[i+1:]); err == nil && w > 0 {
			return arg[:i], w
		}
	}
	return arg, 1
}

func configureSystemSOCKSProxy(host string, port int) (func(), error) {
	if runtime.GOOS != "darwin" {
		return func() {}, nil
	}
	// #nosec G204 -- fixed networksetup binary; args are structured flags validated before this call.
	out, err := exec.Command("networksetup", "-listallnetworkservices").Output()
	if err != nil {
		return nil, fmt.Errorf("networksetup -listallnetworkservices: %w", err)
	}
	var services []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "*") || strings.HasPrefix(line, "An asterisk") {
			continue
		}
		services = append(services, line)
	}
	portStr := strconv.Itoa(port)
	for _, svc := range services {
		// #nosec G204 -- fixed networksetup binary; args are structured flags validated before this call.
		_ = exec.Command("networksetup", "-setsocksfirewallproxy", svc, host, portStr).Run()
		// #nosec G204 -- fixed networksetup binary; args are structured flags validated before this call.
		_ = exec.Command("networksetup", "-setsocksfirewallproxystate", svc, "on").Run()
	}
	cleanup := func() {
		for _, svc := range services {
			// #nosec G204 -- fixed networksetup binary; args are structured flags validated before this call.
			_ = exec.Command("networksetup", "-setsocksfirewallproxystate", svc, "off").Run()
		}
	}
	return cleanup, nil
}

func isValidHostname(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
	}
	return true
}
