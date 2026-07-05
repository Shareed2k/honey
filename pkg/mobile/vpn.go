package mobile

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xjasonlyu/tun2socks/v2/engine"
	"github.com/xjasonlyu/tun2socks/v2/tunnel/statistic"
	"golang.org/x/sys/unix"

	"github.com/shareed2k/honey/internal/cli"
	"github.com/shareed2k/honey/internal/hostapi"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/sshclient"
	"github.com/shareed2k/honey/internal/tun"
)

// vpnSession holds the live VPN run state. Guarded by vpnMu.
type vpnSession struct {
	cancel      context.CancelFunc
	stopForward func()
	pool        *sshclient.SSHPool
}

var (
	vpnMu      sync.Mutex
	vpnCurrent *vpnSession
)

// vpnRequest is the JSON request shared by ResolveExitNode and StartVPN.
type vpnRequest struct {
	ConfigPath            string `json:"config_path"`
	Backends              string `json:"backends"`
	Name                  string `json:"name"`
	NameRegex             string `json:"name_regex"`
	Providers             string `json:"providers"`
	HostIP                string `json:"host_ip"`
	SSHPort               int    `json:"ssh_port"`
	SSHUser               string `json:"ssh_user"`
	SSHIdentityFile       string `json:"ssh_identity_file"`
	SSHIdentityPassphrase string `json:"ssh_identity_passphrase"`
	PoolSize              int    `json:"pool_size"`
	MTU                   int    `json:"mtu"`
}

// resolveExit returns the exit host's record and IP. When the caller already
// knows the IP (host_ip, from a dashboard record), it is used directly and no
// inventory search runs. Otherwise inventory is searched by name and the first
// matching record with a usable IP is returned.
func resolveExit(req vpnRequest) (rec hosts.Record, ip string, sshPort int, err error) {
	if dip := strings.TrimSpace(req.HostIP); dip != "" {
		return hosts.Record{Name: req.Name, PrimaryIP: dip}, dip, req.SSHPort, nil
	}

	searchIn := &hostapi.SearchHostsInput{
		ConfigPath: req.ConfigPath,
		Backends:   req.Backends,
		Name:       req.Name,
		NameRegex:  req.NameRegex,
		Providers:  req.Providers,
		SSHUser:    req.SSHUser,
	}
	out, serr := hostapi.SearchHosts(context.Background(), searchIn, nil, cli.GetSearchRegistry())
	if serr != nil {
		return hosts.Record{}, "", 0, serr
	}
	if len(out.Records) == 0 {
		return hosts.Record{}, "", 0, fmt.Errorf("exit host not found in inventory")
	}
	rec = out.Records[0]
	ip = rec.PrimaryIPTrimmed()
	if ip == "" {
		return hosts.Record{}, "", 0, fmt.Errorf("exit host %q has no IP address", rec.Name)
	}
	if p, ok := hosts.MetaSSHPort(&rec); ok {
		sshPort = p
	}
	return rec, ip, sshPort, nil
}

// ResolveExitNode resolves the SSH exit host so the Android VpnService can build
// its route table (excluding the exit IP) before establish() yields the TUN fd.
// requestJSON: {"config_path","backends","name","ssh_user"}
// returns: {"name","ip","ssh_port","tunnel_routes":["a.b.c.d/32",...]}
// tunnel_routes is every CIDR EXCEPT the exit IP — i.e. the set the caller must
// addRoute() into the TUN so SSH-carrier traffic to the exit stays on the
// physical interface and does not loop through the tunnel.
func ResolveExitNode(requestJSON string) (string, error) {
	var req vpnRequest
	if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
		return "", err
	}
	rec, ip, sshPort, err := resolveExit(req)
	if err != nil {
		return "", err
	}
	tunnelRoutes := tun.ComplementCIDRs([]string{ip + "/32"})
	out := map[string]any{
		"name":          rec.Name,
		"ip":            ip,
		"ssh_port":      sshPort,
		"tunnel_routes": tunnelRoutes,
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// StartVPN attaches the tun2socks engine to an existing VpnService TUN fd and
// pumps it through a fresh SOCKS5-over-SSH tunnel to the requested exit host.
// Non-blocking; lifecycle and traffic are streamed via cb. Returns an error if a
// VPN session is already running or initial connect fails.
func StartVPN(tunFd int, requestJSON string, cb VPNCallback) error {
	var req vpnRequest
	if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
		return err
	}
	if req.PoolSize <= 0 {
		req.PoolSize = 2
	}
	if req.MTU <= 0 {
		req.MTU = 1500
	}

	vpnMu.Lock()
	if vpnCurrent != nil {
		vpnMu.Unlock()
		return fmt.Errorf("vpn already running")
	}
	vpnMu.Unlock()

	// Validate the TUN fd up front: engine.Start() aborts the whole process via
	// log.Fatalf if the netstack device fails to open, so reject a bad fd here
	// with a recoverable error instead.
	if _, ferr := unix.FcntlInt(uintptr(tunFd), unix.F_GETFD, 0); ferr != nil {
		emitState(cb, "error")
		return fmt.Errorf("invalid tun fd %d: %w", tunFd, ferr)
	}

	// Duplicate the TUN fd so the Go tun2socks engine owns an independent fd.
	// The Android ParcelFileDescriptor keeps and closes the original; the engine
	// closes this dup on Stop(). The gvisor fdbased device closes the exact fd it
	// is handed (no internal dup), so sharing the caller's fd would double-close
	// it — bionic fdsan turns that into a SIGABRT on disconnect.
	dupFd, derr := unix.Dup(tunFd)
	if derr != nil {
		emitState(cb, "error")
		return fmt.Errorf("dup tun fd %d: %w", tunFd, derr)
	}
	engineOwnsFd := false
	defer func() {
		if !engineOwnsFd {
			_ = unix.Close(dupFd)
		}
	}()

	emitState(cb, "resolving")
	_, ip, sshPort, err := resolveExit(req)
	if err != nil {
		emitState(cb, "error")
		return err
	}

	emitState(cb, "connecting")
	ctx, cancel := context.WithCancel(context.Background())

	user := strings.TrimSpace(req.SSHUser)
	if user == "" {
		user = defaultSSHUser(req.ConfigPath)
	}
	// Authenticate with the key held in memory — it is never written to disk.
	keyPEM := strings.TrimSpace(req.SSHIdentityFile)
	dialFn := func() (*sshclient.HoneyClient, error) {
		if keyPEM != "" {
			return sshclient.DialHoneyClientWithKey(user, ip, sshPort, []byte(keyPEM), req.SSHIdentityPassphrase)
		}
		return sshclient.DialHoneyClient(user, ip, sshPort, "")
	}
	pool, err := sshclient.NewSSHPool(ctx, req.PoolSize, dialFn)
	if err != nil {
		cancel()
		emitState(cb, "error")
		return fmt.Errorf("ssh pool: %w", err)
	}

	socksHost, socksPort, stopForward, err := sshclient.StartDynamicForwardMulti(
		ctx,
		[]sshclient.WeightedClient{{Client: pool, Weight: 1}},
		"127.0.0.1", 0,
	)
	if err != nil {
		pool.Close()
		cancel()
		emitState(cb, "error")
		return fmt.Errorf("start SOCKS5 proxy: %w", err)
	}

	// Reset cumulative counters so this session starts from zero.
	statistic.DefaultManager.ResetStatistic()

	engine.Insert(&engine.Key{
		Device: "fd://" + strconv.Itoa(dupFd),
		Proxy:  fmt.Sprintf("socks5://%s:%d", socksHost, socksPort),
		MTU:    req.MTU,
		// DNS is tunneled as DNS-over-TCP (one SSH channel per query); a short
		// UDP idle timeout recycles those channels so they don't pile up against
		// the SOCKS server's channel cap.
		UDPTimeout: 10 * time.Second,
		LogLevel:   "warning",
	})
	engine.Start()
	engineOwnsFd = true // engine.Stop() now closes dupFd

	vpnMu.Lock()
	vpnCurrent = &vpnSession{
		cancel:      cancel,
		stopForward: stopForward,
		pool:        pool,
	}
	vpnMu.Unlock()

	emitState(cb, "connected")
	go pumpStats(ctx, cb)
	return nil
}

// StopVPN tears down the active VPN session. Safe to call when none is running.
func StopVPN() error {
	vpnMu.Lock()
	sess := vpnCurrent
	vpnCurrent = nil
	vpnMu.Unlock()
	if sess == nil {
		return nil
	}
	engine.Stop()
	if sess.stopForward != nil {
		sess.stopForward()
	}
	if sess.pool != nil {
		sess.pool.Close()
	}
	if sess.cancel != nil {
		sess.cancel()
	}
	return nil
}

// pumpStats emits throughput snapshots every second until ctx is cancelled.
func pumpStats(ctx context.Context, cb VPNCallback) {
	if cb == nil {
		return
	}
	start := time.Now()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snap := statistic.DefaultManager.Snapshot()
			up, down := statistic.DefaultManager.Now()
			b, err := json.Marshal(map[string]any{
				"up_total":   snap.UploadTotal,
				"down_total": snap.DownloadTotal,
				"up_rate":    up,
				"down_rate":  down,
				"uptime_s":   int64(time.Since(start).Seconds()),
			})
			if err == nil {
				cb.OnStats(string(b))
			}
		}
	}
}

func emitState(cb VPNCallback, state string) {
	if cb != nil {
		cb.OnState(state)
	}
}
