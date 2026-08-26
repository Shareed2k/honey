package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/portmux"
)

// webSSHMux is `honey web --ssh-mux`: the inbound SSH gateway served on the web
// UI's own port, each connection routed by its first bytes (see
// internal/portmux). A nil *webSSHMux means the flag is off, and every method
// here is nil-safe so runWeb carries one code path instead of two.
type webSSHMux struct {
	mux *portmux.Mux
	gw  *gatewayBuild
}

// newWebSSHMux returns nil when --ssh-mux is off, leaving the web server to
// bind its own listener exactly as before.
//
// When on, the gateway is built BEFORE the socket is bound: a misconfiguration
// (no trusted CA, unreadable host key) must fail the command outright rather
// than leave a web server running with a broken SSH half — and it must fail
// before the browser is opened.
func newWebSSHMux(cmd *cobra.Command, listen string) (*webSSHMux, error) {
	if !webSSHMuxFlag {
		return nil, nil
	}
	gw, err := buildGatewayServer(cmd, listen)
	if err != nil {
		return nil, fmt.Errorf("--ssh-mux: %w", err)
	}
	base, err := net.Listen("tcp", listen)
	if err != nil {
		gw.Close()
		return nil, err
	}
	return &webSSHMux{mux: portmux.New(base), gw: gw}, nil
}

// HTTPListener is the half the web server serves — nil when the flag is off,
// which webserver.Options.Listener reads as "bind ListenAddr yourself".
func (m *webSSHMux) HTTPListener() net.Listener {
	if m == nil {
		return nil
	}
	return m.mux.HTTP
}

// Serve starts the SSH gateway and the demultiplexer, and returns a stop
// function that closes the port and waits for both loops to unwind. The caller
// defers stop, so the sequence on shutdown is: web server returns, port
// closes, both accept loops see it, stop returns.
//
// Note the coupling in the other direction too: cmux's sub-listeners share the
// base socket, so the web server closing ITS listener during shutdown takes
// the whole port — SSH included — down with it. That is intended here: one
// port, one lifetime.
func (m *webSSHMux) Serve(ctx context.Context) (stop func()) {
	if m == nil {
		return func() {}
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := m.gw.Server.Serve(ctx, m.mux.SSH); err != nil && ctx.Err() == nil {
			zap.L().Error("ssh-mux: gateway stopped", zap.Error(err))
		}
	}()
	go func() {
		defer wg.Done()
		if err := m.mux.Serve(); err != nil && ctx.Err() == nil {
			zap.L().Error("ssh-mux: port demultiplexer stopped", zap.Error(err))
		}
	}()
	return func() {
		_ = m.mux.Close()
		wg.Wait()
	}
}

// Close releases what newWebSSHMux acquired but Serve never took over (the
// gateway's audit sink). Safe to defer immediately after construction.
func (m *webSSHMux) Close() {
	if m == nil {
		return
	}
	m.gw.Close()
}

// PrintBanner reports the SSH half on the same startup block as the web UI's
// URL, since a single port serving two protocols is the surprising part.
func (m *webSSHMux) PrintBanner(listen string) {
	if m == nil {
		return
	}
	_, port, _ := net.SplitHostPort(listen)
	_, _ = fmt.Fprintf(os.Stdout,
		"  SSH:   multiplexed on this port (trusted CAs: %d) — ssh -p %s <actor>@<host> <resource>\n",
		m.gw.TrustedCAs, port)
}
