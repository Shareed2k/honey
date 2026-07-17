package honeyprovider

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/shareed2k/honey/internal/tun"
)

// StartTunForward gives a local TUN device exit traffic through the
// upstream Honey proxy. It is a COMPOSITION of two existing pieces, not a
// new packet-forwarding endpoint: StartDynamicForward opens a local SOCKS5
// proxy whose dials go through the upstream seam (c.dialer()), and
// internal/tun.Run shells out to the tun2proxy binary to create and route a
// local TUN device against that SOCKS5 proxy -- exactly the mechanism
// `honey egress --tun` uses (internal/cli/egress.go), just with the SOCKS5
// backing it produced by the Honey proxy instead of sshclient's SSH-backed
// dynamic forward.
//
// Signature divergence from hostexec.HostClient: this method's alias,
// sshPort, tunLocal, and tunRemote parameters are inherited from
// sshclient.StartTunForward, which runs `ssh -w tunLocal:tunRemote -N` to
// negotiate a kernel-level point-to-point TUN device directly with the
// remote sshd (see internal/sshclient/forward_start.go). The upstream Honey
// proxy has no equivalent primitive -- it only exposes a byte-stream dial
// seam over its own transport -- so none of those four parameters have a
// meaning here and this implementation ignores them entirely. Routing
// (exit host name, the SSH/record peer IP auto-bypassed from the tunnel) is
// derived from c.record instead. The signature is kept identical only so
// *Client continues to satisfy hostexec.HostClient.
//
// Every goroutine this starts (the dynamic forward's internals, and the
// goroutine running tun.Run) is torn down by the returned stop(), which is
// idempotent: it cancels the tun2proxy run's context (causing tun.Run to
// SIGTERM the tun2proxy subprocess and return), calls the dynamic forward's
// stop(), and waits for the tun.Run goroutine to exit.
func (c *Client) StartTunForward(ctx context.Context, _ string, _ string, _ int, _, _ int) (tunName string, stop func(), err error) {
	getuid := c.getuidFn
	if getuid == nil {
		getuid = os.Getuid
	}
	if getuid() != 0 {
		return "", nil, fmt.Errorf("honey upstream tun forward requires root (re-run with sudo)")
	}

	startDynamicForward := c.startDynamicForwardFn
	if startDynamicForward == nil {
		startDynamicForward = c.StartDynamicForward
	}
	host, port, dfStop, err := startDynamicForward(ctx, "127.0.0.1", 0)
	if err != nil {
		return "", nil, fmt.Errorf("honey upstream tun forward: start dynamic forward: %w", err)
	}

	runTun := c.tunRunFn
	if runTun == nil {
		runTun = tun.Run
	}

	tunCtx, tunCancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = runTun(tunCtx, tun.Config{
			SOCKSHost: host,
			SOCKSPort: port,
			HostName:  c.record.Name,
			SSHIPs:    []string{c.record.PrimaryIP},
		})
	}()

	stop = sync.OnceFunc(func() {
		tunCancel()
		dfStop()
		wg.Wait()
	})

	// internal/tun.Run shells out to the tun2proxy binary, which owns
	// device creation/naming itself (via its --setup flag) and never
	// reports the resulting interface name back to Run's caller. "tun-honey"
	// is a stable, descriptive placeholder -- not the OS-assigned device
	// name (e.g. "utun7" / "tun0") -- surfaced here only so callers get a
	// non-empty, recognizable identifier.
	return "tun-honey", stop, nil
}
