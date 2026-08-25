// Package portmux serves SSH and HTTP on a single TCP port by routing each
// connection on its first bytes: an SSH client opens with the identification
// string "SSH-2.0-…" (RFC 4253 §4.2), which no HTTP request can begin with, so
// the two protocols are distinguishable before either server sees the socket.
//
// This is protocol demultiplexing, NOT stream multiplexing: a browser and a
// native ssh client speak their own protocols and cannot be asked to wrap
// themselves in a mux framing (yamux and friends need both ends to agree —
// which is why honey uses one for its own mesh links and peeking here).
//
// Two constraints come with the trick, and both are load-bearing:
//
//   - It works at L4 only. Anything that terminates HTTP in front of this port
//     (an ALB in HTTP mode, a CDN) will not pass an SSH connection through.
//     TCP passthrough is required.
//   - It assumes the SSH client sends its identification string without waiting
//     for the server's. RFC 4253 has both sides send immediately and OpenSSH
//     does; a hypothetical client that waited for the server banner first would
//     stall until ReadTimeout and then be routed to the HTTP half.
package portmux

import (
	"net"
	"time"

	"github.com/soheilhy/cmux"
)

// sniffTimeout bounds how long a connection may stay unclassified. Matching
// requires reading the client's first bytes, so a peer that connects and says
// nothing occupies a matcher until this expires — without a bound, cheap idle
// connections would pin resources indefinitely. On expiry cmux routes the
// connection to the fallback (HTTP) listener, where the http.Server's own
// ReadHeaderTimeout takes over.
const sniffTimeout = 15 * time.Second

// Mux splits one bound listener into an SSH half and an HTTP half.
type Mux struct {
	base net.Listener
	mux  cmux.CMux

	// SSH receives connections whose first bytes are an SSH identification
	// string; HTTP receives everything else (including unclassifiable ones).
	SSH  net.Listener
	HTTP net.Listener
}

// New wraps an already-bound listener. Both sub-listeners are created up front
// (cmux requires every matcher to be registered before Serve) and neither
// accepts anything until Serve runs.
func New(base net.Listener) *Mux {
	m := cmux.New(base)
	m.SetReadTimeout(sniffTimeout)
	// A failed match is one bad client, not a dead listener: returning true keeps
	// the accept loop running instead of tearing down both servers.
	m.HandleError(func(error) bool { return true })
	return &Mux{
		base: base,
		mux:  m,
		SSH:  m.Match(cmux.PrefixMatcher("SSH-")),
		HTTP: m.Match(cmux.Any()),
	}
}

// Serve runs the accept-and-route loop until Close (or a base listener error).
// It returns cmux.ErrListenerClosed after Close, which callers treat as a clean
// shutdown.
func (m *Mux) Serve() error { return m.mux.Serve() }

// Close closes the underlying listener, which unblocks Serve. The sub-listeners
// are closed by cmux in turn, so each server's own accept loop unwinds.
func (m *Mux) Close() error { return m.base.Close() }

// Addr is the bound address of the underlying listener.
func (m *Mux) Addr() net.Addr { return m.base.Addr() }
