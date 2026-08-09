package sshgateway

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/guardrails"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/policy"
)

// handshakeTimeout bounds the SSH handshake so a slow or malicious client
// cannot hold a per-connection goroutine open indefinitely before it
// authenticates. It is cleared once the handshake completes.
const handshakeTimeout = 30 * time.Second

// shutdownGrace bounds how long Start waits for in-flight connections to unwind
// after the context is cancelled. A session blocked in a slow operation (e.g.
// resolving a resource against an unreachable backend, or dialing an unreachable
// target) must never make Ctrl+C hang: once this elapses Start returns anyway and
// the lingering goroutines are terminated when the process exits.
const shutdownGrace = 5 * time.Second

// Options configures a gateway Server. Zero AuditSink/Enforcer/RecordDir are
// safe: audit is a no-op, policy allows (subject to the command-risk floor),
// and sessions are not recorded.
type Options struct {
	// ListenAddr is the host:port the gateway listens on.
	ListenAddr string
	// HostKey is the gateway's SSH host key (see LoadOrCreateHostKey).
	HostKey ssh.Signer
	// TrustedCAs are the SSH CA public keys whose user certificates are accepted.
	TrustedCAs []ssh.PublicKey
	// UserAttr labels which identity attribute names the actor (audit only).
	UserAttr string
	// CertAttr selects the certificate field used as the actor ("principal"|"key_id").
	CertAttr string
	// DefaultSSHUser is the fallback login user for the target host when the
	// record carries no ssh_user meta. The authenticated actor (cert principal)
	// is the authorization identity, not necessarily the target account, so the
	// target login is resolved record.Meta["ssh_user"] -> DefaultSSHUser -> actor.
	DefaultSSHUser string
	// Enforcer is the OPA policy enforcer; nil is a no-op allow.
	Enforcer *policy.Enforcer
	// Guardrails is the deterministic operator-defined guardrail floor applied to
	// every gated command (interactive per-line assessment and ad-hoc exec),
	// evaluated before OPA. nil (or empty) is a no-op.
	Guardrails *guardrails.Ruleset
	// AuditSink receives audit events; nil becomes a no-op sink.
	AuditSink audit.Sink
	// RecordDir is where session recordings are written; empty disables recording.
	RecordDir string
	// MaskRules redacts secrets in the target→client output — both in the live
	// stream and in the recording — via a streaming redactor (see mask.go). Nil
	// disables masking. Build it with NewMaskRuleset.
	MaskRules *MaskRuleset
	// Records resolves the current inventory. Called per session (the CLI wraps
	// it with a short TTL cache).
	Records func(ctx context.Context) ([]hosts.Record, error)
	// ExecRegistry resolves a record to a provider executor (docker/k8s/mesh/ssh)
	// through the shared hostexec seam, so the gateway proxies the same targets as
	// the web terminal. nil = SSH-only via the ui helpers, the pre-Phase-F
	// behavior (backwards compatible).
	ExecRegistry hostexec.Registry
	// GuardMode selects the best-effort per-command interactive guardrail:
	// "off" (default), "audit", or "enforce". In interactive shells it runs the
	// same risk+policy assessment exec uses against each command line the user
	// types; enforce blocks a denied line locally. This is defense-in-depth
	// layered on top of the authoritative target-side command-risk gate, not a
	// replacement for it — see guardReader for the caveats. Empty/unknown = off.
	GuardMode string
	// DisableAuth accepts any client without a certificate (dev only).
	DisableAuth bool
}

// Server is an inbound SSH gateway. Its surface is New + Start; all inbound-SSH
// concerns (cert auth, resource routing, channel handling) are hidden behind it.
type Server struct {
	opts      Options
	sshConfig *ssh.ServerConfig
	log       *zap.Logger

	mu    sync.Mutex
	addr  string
	ready chan struct{}
}

// New validates opts and builds the server. It requires at least one trusted CA
// unless DisableAuth is set (deny-by-default).
func New(opts Options) (*Server, error) {
	if opts.HostKey == nil {
		return nil, fmt.Errorf("sshgateway: host key is required")
	}
	if strings.TrimSpace(opts.ListenAddr) == "" {
		return nil, fmt.Errorf("sshgateway: listen address is required")
	}
	if opts.Records == nil {
		return nil, fmt.Errorf("sshgateway: records provider is required")
	}
	if !opts.DisableAuth && len(opts.TrustedCAs) == 0 {
		return nil, fmt.Errorf("sshgateway: at least one trusted CA is required unless auth is disabled")
	}
	if opts.AuditSink == nil {
		opts.AuditSink = audit.NewNoopSink()
	}
	sshCfg, err := buildServerConfig(opts.HostKey, opts.DisableAuth, certAuthConfig{
		trustedCAs: opts.TrustedCAs,
		userAttr:   opts.UserAttr,
		certAttr:   opts.CertAttr,
	})
	if err != nil {
		return nil, fmt.Errorf("sshgateway: %w", err)
	}
	return &Server{
		opts:      opts,
		sshConfig: sshCfg,
		log:       zap.L().Named("sshgateway"),
		ready:     make(chan struct{}),
	}, nil
}

// Ready is closed once the listener is bound and Addr is populated.
func (s *Server) Ready() <-chan struct{} { return s.ready }

// Addr returns the bound listen address once the server is ready (useful when
// ListenAddr uses port 0).
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

// Start binds the listener and serves connections until ctx is cancelled. All
// per-connection and per-session goroutines are drained before it returns.
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.opts.ListenAddr)
	if err != nil {
		return fmt.Errorf("sshgateway: listen %q: %w", s.opts.ListenAddr, err)
	}
	s.mu.Lock()
	s.addr = ln.Addr().String()
	s.mu.Unlock()
	close(s.ready)

	// runCtx is cancelled on every return path (ctx cancel or accept error) so
	// the listener-closer goroutine and all conns unwind — no goroutine leaks.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	closerDone := make(chan struct{})
	go func() {
		defer close(closerDone)
		<-runCtx.Done()
		_ = ln.Close()
	}()

	var (
		wg        sync.WaitGroup
		acceptErr error
	)
	for {
		raw, aerr := ln.Accept()
		if aerr != nil {
			if ctx.Err() == nil {
				acceptErr = fmt.Errorf("sshgateway: accept: %w", aerr)
			}
			break
		}
		wg.Add(1)
		go func(c net.Conn) {
			defer wg.Done()
			s.serveConn(runCtx, c)
		}(raw)
	}
	cancel()
	// Bounded drain: connections get a grace period to unwind after their
	// contexts and the listener are closed, but shutdown never blocks
	// indefinitely on a stuck session (see shutdownGrace).
	drained := make(chan struct{})
	go func() { wg.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(shutdownGrace):
		s.log.Warn("sshgateway: shutdown grace elapsed; exiting with connections still draining")
	}
	<-closerDone
	return acceptErr
}

// serveConn performs the deadline-bounded handshake, then serves session
// channels until the client hangs up or ctx is cancelled.
func (s *Server) serveConn(ctx context.Context, raw net.Conn) {
	_ = raw.SetDeadline(time.Now().Add(handshakeTimeout))
	sc, chans, reqs, err := ssh.NewServerConn(raw, s.sshConfig)
	if err != nil {
		_ = raw.Close()
		return
	}
	_ = raw.SetDeadline(time.Time{})

	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		<-connCtx.Done()
		_ = sc.Close()
	}()

	go ssh.DiscardRequests(reqs)

	actor := "anonymous"
	if sc.Permissions != nil {
		if a := sc.Permissions.Extensions[extActor]; strings.TrimSpace(a) != "" {
			actor = a
		}
	}

	var wg sync.WaitGroup
	for newCh := range chans {
		switch newCh.ChannelType() {
		case "session":
			wg.Add(1)
			go func(nc ssh.NewChannel) {
				defer wg.Done()
				s.serveSession(connCtx, nc, actor)
			}(newCh)
		case "direct-tcpip":
			wg.Add(1)
			go func(nc ssh.NewChannel) {
				defer wg.Done()
				s.serveDirectTCPIP(connCtx, nc, actor)
			}(newCh)
		default:
			_ = newCh.Reject(ssh.UnknownChannelType, "unsupported channel type")
		}
	}
	cancel()
	wg.Wait()
	<-closed
}
