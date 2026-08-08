package sshgateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"

	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/ui"
)

// directTCPIPPayload is the RFC 4254 "direct-tcpip" channel-open request, decoded
// with ssh.Unmarshal (field order, not names, is what matters): destination
// address, destination port, originator address, originator port.
type directTCPIPPayload struct {
	DestHost string
	DestPort uint32
	OrigHost string
	OrigPort uint32
}

// closeWriter is the half-close capability of a stream. ssh.Channel implements
// it; a leaf-dialed net.Conn may or may not, so bridge guards the assertion.
type closeWriter interface {
	CloseWrite() error
}

// serveDirectTCPIP handles a client "direct-tcpip" channel — what
// `ssh -L localport:<resource>:<port>` opens — delivering the DB port-forward
// (`ssh -N -L 15432:postgres-demo:5432 alice@gw`).
//
// MVP dest mapping: the channel's destination host is treated as the inventory
// resource name and the destination port as a service bound to that host's
// loopback. The gateway resolves the resource, SSH-dials the target host (Phase A
// helpers), opens client.Dial("tcp", "127.0.0.1:<destPort>") on the target, and
// bridges the client channel to it. A DB bound to loopback on the target is the
// common secure case. SSH-record targets only (k8s pods are rejected upstream by
// DialSSHLeafForRecord). An unresolved destination host is rejected.
func (s *Server) serveDirectTCPIP(ctx context.Context, newCh ssh.NewChannel, actor string) {
	var p directTCPIPPayload
	if err := ssh.Unmarshal(newCh.ExtraData(), &p); err != nil {
		_ = newCh.Reject(ssh.ConnectionFailed, "bad direct-tcpip payload")
		return
	}
	destHost := p.DestHost
	destPort := int(p.DestPort)

	// In every rejection path the audit event is emitted BEFORE newCh.Reject: the
	// two run in this goroutine's program order, so a client that observes the
	// reject is guaranteed the audit has already been recorded (no race between
	// the client-side channel-open failure and the server-side audit).
	if err := s.gateTunnel(ctx, actor, "tcp", destHost, destPort); err != nil {
		s.audit(ctx, audit.Event{Actor: actor, Action: "tunnel", Target: destHost, Decision: "deny", DenyReason: err.Error()})
		_ = newCh.Reject(ssh.Prohibited, err.Error())
		return
	}

	rec, err := s.resolveResource(ctx, destHost)
	if err != nil {
		s.audit(ctx, audit.Event{Actor: actor, Action: "tunnel", Target: destHost, Decision: "deny", DenyReason: err.Error()})
		_ = newCh.Reject(ssh.ConnectionFailed, err.Error())
		return
	}

	// Dial the tunnel upstream. With a registry (the CLI default) route through the
	// hostexec seam so docker (nc/socat in the container), k8s (SPDY port-forward),
	// and mesh records forward like the web tunnel; the seam's SSH fallback dials
	// the leaf for plain hosts. A nil registry keeps the pre-Phase-F leaf path. On
	// the registry path the returned conn owns its transport, so the teardown's
	// upstream.Close() releases it and cleanup stays a no-op; on the leaf path
	// cleanup releases the borrowed SSH client exactly as before.
	var (
		upstream net.Conn
		cleanup  = func() {}
	)
	if s.opts.ExecRegistry != nil {
		ex := s.opts.ExecRegistry.ForRecord(rec)
		upstream, err = ex.DialUpstream(ctx, s.targetUser(rec, actor), rec, net.JoinHostPort("127.0.0.1", strconv.Itoa(destPort)))
		if err != nil {
			s.audit(ctx, audit.Event{Actor: actor, Action: "tunnel", Target: rec.Name, Decision: "deny", DenyReason: err.Error()})
			_ = newCh.Reject(ssh.ConnectionFailed, fmt.Sprintf("dial service: %v", err))
			return
		}
	} else {
		client, cl, derr := ui.DialSSHLeafForRecord(s.targetUser(rec, actor), rec)
		if derr != nil {
			s.audit(ctx, audit.Event{Actor: actor, Action: "tunnel", Target: rec.Name, Decision: "deny", DenyReason: derr.Error()})
			_ = newCh.Reject(ssh.ConnectionFailed, fmt.Sprintf("connect: %v", derr))
			return
		}
		upstream, err = client.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(destPort)))
		if err != nil {
			s.audit(ctx, audit.Event{Actor: actor, Action: "tunnel", Target: rec.Name, Decision: "deny", DenyReason: err.Error()})
			_ = newCh.Reject(ssh.ConnectionFailed, fmt.Sprintf("dial service: %v", err))
			cl()
			return
		}
		cleanup = cl
	}

	ch, reqs, err := newCh.Accept()
	if err != nil {
		_ = upstream.Close()
		cleanup()
		return
	}
	go ssh.DiscardRequests(reqs)

	s.audit(ctx, audit.Event{
		Actor:    actor,
		Action:   "tunnel",
		Target:   rec.Name,
		Decision: "allow",
		Extra:    map[string]string{"dest_port": strconv.Itoa(destPort)},
	})

	bridge(ctx, ch, upstream)
	_ = ch.Close()
	_ = upstream.Close()
	cleanup()
}

// gateTunnel asks OPA whether actor may have the gateway dial a tunnel to
// host:port on their behalf (action "tunnel"). It mirrors the web
// ForwardingAPI.gateTunnel input shape exactly so a single policy governs both
// front-ends. A nil enforcer always allows; a deny returns the policy reason.
func (s *Server) gateTunnel(ctx context.Context, actor, scheme, host string, port int) error {
	if s.opts.Enforcer == nil {
		return nil
	}
	d, err := s.opts.Enforcer.Evaluate(ctx, map[string]any{
		"action": "tunnel",
		"actor":  actor,
		"target": map[string]any{
			"scheme": scheme,
			"dest":   net.JoinHostPort(host, strconv.Itoa(port)),
			"host":   host,
			"port":   port,
		},
	})
	if err != nil {
		return fmt.Errorf("policy: %w", err)
	}
	if !d.Allow {
		reason := d.DenyReason
		if reason == "" {
			reason = "forbidden by policy"
		}
		return errors.New(reason)
	}
	return nil
}

// bridge copies bytes in both directions between a and b until both directions
// finish or ctx is cancelled, then returns. When one direction reaches EOF it
// half-closes the peer's write side so the peer sees EOF and can drain and finish
// its own direction; a side that cannot half-close is fully closed, which also
// unblocks the paired copy. A ctx-cancel watcher force-closes both to unblock any
// in-flight Read. No goroutine outlives the return.
func bridge(ctx context.Context, a, b io.ReadWriteCloser) {
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = a.Close()
			_ = b.Close()
		case <-watchDone:
		}
	}()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(a, b)
		halfCloseWrite(a)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(b, a)
		halfCloseWrite(b)
	}()
	wg.Wait()
}

// halfCloseWrite shuts down the write half of c so a peer blocked on Read sees
// EOF. If c has no half-close it is closed fully.
func halfCloseWrite(c io.ReadWriteCloser) {
	if cw, ok := c.(closeWriter); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = c.Close()
}
