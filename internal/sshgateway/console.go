package sshgateway

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/proxmoxprovider"
	"github.com/shareed2k/honey/internal/provider/truenasprovider"
	"github.com/shareed2k/honey/internal/pvelxc"
	"github.com/shareed2k/honey/internal/truenasshell"
)

// isConsoleTarget reports whether rec is an interactive-console-only target: a
// Proxmox LXC/QEMU serial console or a TrueNAS middleware shell. Such targets are
// reached over a provider websocket, not a raw SSH leaf, so they support an
// interactive shell (ssh -t) but not ad-hoc exec or port-forward.
func isConsoleTarget(rec hosts.Record) bool {
	return pvelxc.ShouldUsePVETTY(rec) ||
		truenasshell.ShouldUseTrueNASShell(rec, truenasshell.ConsoleTrueNASAPI)
}

// runProxmoxConsole opens the record's Proxmox serial console (termproxy +
// vncwebsocket, shared with the web terminal) and bridges the caller's streams to
// it. It returns the bridge's error (nil on a clean exit); the caller
// (runInteractive) owns the recorder, mask flush, and interactive_exit audit.
// Open-time failures are surfaced to the client on stdout before being returned.
func (s *Server) runProxmoxConsole(ctx context.Context, rec hosts.Record, stdin io.Reader, stdout io.Writer, cols, rows int, resize <-chan [2]int) error {
	b, ok := proxmoxprovider.BackendByName(rec.Meta["backend_name"])
	if !ok {
		err := fmt.Errorf("proxmox backend not configured for %q", rec.Name)
		fmt.Fprintf(stdout, "error: %v\n", err)
		return err
	}
	node := strings.TrimSpace(rec.Meta["node"])
	vmid, err := strconv.Atoi(strings.TrimSpace(rec.Meta["vmid"]))
	if err != nil || vmid <= 0 || node == "" {
		cerr := fmt.Errorf("proxmox record %q missing node or vmid", rec.Name)
		fmt.Fprintf(stdout, "error: %v\n", cerr)
		return cerr
	}
	guest := strings.TrimSpace(rec.Meta["kind"])
	if guest == "" {
		guest = "lxc"
	}

	sess, err := pvelxc.OpenSession(ctx, b, guest, node, vmid, rows, cols)
	if err != nil {
		fmt.Fprintf(stdout, "error: %v\n", err)
		return fmt.Errorf("proxmox console: %w", err)
	}
	defer func() { _ = sess.Close() }()

	return pvelxc.BridgeStreams(ctx, sess, stdin, stdout, resize, nil)
}

// runTrueNASConsole opens the record's TrueNAS middleware shell
// (/websocket/shell, shared with the web terminal) and bridges the caller's
// streams to it. It returns the bridge's error (nil on a clean exit); the caller
// (runInteractive) owns the recorder, mask flush, and interactive_exit audit.
// Open-time failures are surfaced to the client on stdout before being returned.
func (s *Server) runTrueNASConsole(ctx context.Context, rec hosts.Record, stdin io.Reader, stdout io.Writer, cols, rows int, resize <-chan [2]int) error {
	b, ok := truenasprovider.BackendByName(rec.Meta["backend_name"])
	if !ok {
		err := fmt.Errorf("truenas backend not configured for %q", rec.Name)
		fmt.Fprintf(stdout, "error: %v\n", err)
		return err
	}

	sess, err := truenasshell.OpenSession(ctx, b, rec, rows, cols)
	if err != nil {
		fmt.Fprintf(stdout, "error: %v\n", err)
		return fmt.Errorf("truenas console: %w", err)
	}
	defer func() { _ = sess.Close() }()

	return truenasshell.BridgeStreams(ctx, sess, stdin, stdout, resize, nil)
}
