package tun

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"

	gossh "golang.org/x/crypto/ssh"
)

// Config holds everything Run needs to start tun2proxy.
type Config struct {
	SOCKSHost     string
	SOCKSPort     int
	HostName      string   // display name for the exit node(s)
	SSHIPs        []string // SSH peer IPs — auto-bypassed as /32
	ExtraBypasses []string // user --bypass CIDRs
	Nets          []string // --nets: route only these (complement becomes bypasses)
}

// Run starts tun2proxy-bin as a subprocess using cfg. Blocks until ctx is
// cancelled or the process exits. Sends SIGTERM on cancellation so tun2proxy
// can restore routes before exiting.
func Run(ctx context.Context, cfg Config) error {
	bin, err := findBin()
	if err != nil {
		return err
	}

	args := []string{
		"--proxy", fmt.Sprintf("socks5://%s:%d", cfg.SOCKSHost, cfg.SOCKSPort),
		"--setup",
		"--ipv6-enabled",
		"--dns", "over-tcp",
		"--dns-addr", "8.8.8.8",
		"--verbosity", "warn",
	}
	for _, ip := range cfg.SSHIPs {
		if ip != "" {
			args = append(args, "--bypass", ip+"/32")
		}
	}
	for _, cidr := range cfg.ExtraBypasses {
		if cidr != "" {
			args = append(args, "--bypass", cidr)
		}
	}
	for _, cidr := range ComplementCIDRs(cfg.Nets) {
		args = append(args, "--bypass", cidr)
	}

	fmt.Printf("Starting tun2proxy (VPN exit via %s)...\nPress Ctrl+C to stop.\n", cfg.HostName)
	// #nosec G204 -- bin is resolved via LookPath or a hardcoded trusted path.
	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("tun2proxy start: %w", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-ctx.Done():
		_ = cmd.Process.Signal(syscall.SIGTERM)
		<-done
		return nil
	case err := <-done:
		if err != nil {
			return fmt.Errorf("tun2proxy: %w", err)
		}
		return nil
	}
}

// QueryRemoteNets SSHes to the host and returns its non-default routes as CIDRs.
// Mirrors sshuttle's server.py list_routes(): `ip route` (Linux) or `netstat -rn` (macOS/BSD).
func QueryRemoteNets(client *gossh.Client) []string {
	sess, err := client.NewSession()
	if err != nil {
		return nil
	}
	defer sess.Close()
	out, err := sess.Output("ip route show 2>/dev/null || netstat -rn 2>/dev/null")
	if err != nil || len(out) == 0 {
		return nil
	}
	return parseRouteOutput(string(out))
}

func parseRouteOutput(output string) []string {
	var nets []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "default") ||
			strings.HasPrefix(line, "Destination") || strings.HasPrefix(line, "Routing") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		cidr := normalizeCIDR(fields[0], fields)
		if cidr == "" || strings.HasPrefix(cidr, "0.") || strings.HasPrefix(cidr, "127.") {
			continue
		}
		nets = append(nets, cidr)
	}
	return nets
}

func normalizeCIDR(s string, fields []string) string {
	if _, cidr, err := net.ParseCIDR(s); err == nil {
		return cidr.String()
	}
	// netstat form: dest in fields[0], netmask in fields[2]
	if len(fields) >= 3 {
		ip := net.ParseIP(fields[0])
		mask := net.ParseIP(fields[2])
		if ip != nil && mask != nil && mask.To4() != nil {
			m := net.IPMask(mask.To4())
			return (&net.IPNet{IP: ip.To4().Mask(m), Mask: m}).String()
		}
	}
	return ""
}

func findBin() (string, error) {
	for _, name := range []string{"tun2proxy-bin", "tun2proxy"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	for _, p := range []string{
		"/opt/homebrew/bin/tun2proxy-bin",
		"/usr/local/bin/tun2proxy-bin",
		"/usr/local/bin/tun2proxy",
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("tun2proxy not found")
}
