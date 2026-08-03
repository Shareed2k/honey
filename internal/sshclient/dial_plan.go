package sshclient

import (
	"net"
	"strconv"
	"time"

	"github.com/melbahja/goph"
	"golang.org/x/crypto/ssh"
)

// sshHandshakeTimeout bounds the SSH handshake (banner, key exchange, and auth)
// so a host that accepts TCP but never drives the protocol forward can't wedge a
// dialer forever. It is deliberately much larger than goph.DefaultTimeout (the
// TCP-connect bound): the handshake+auth legitimately takes longer than the TCP
// connect — a slow sshd banner, key exchange over a high-latency link, or the
// agent/key auth round-trips — so reusing the 20s connect timeout tripped on
// slow-but-working hosts (a random "read tcp ...:22: i/o timeout"). This value
// only needs to be generous enough to never fire on a working host while still
// capping a truly stuck handshake.
const sshHandshakeTimeout = 60 * time.Second

// hopPlan is one resolved node in a dial chain: the address to dial plus the
// host's SSH config (used for the per-hop user and host-key callback). The
// config is resolved exactly once and reused for both auth-identity collection
// and dialing.
type hopPlan struct {
	host string
	port int
	user string
	cfg  *hostSSHConfig
}

func (h hopPlan) addr() string { return net.JoinHostPort(h.host, strconv.Itoa(h.port)) }

// dialPlan is the fully-resolved recipe for one dial: the ordered ProxyJump hops
// (empty for a direct dial), the final target, and the shared auth. It is
// computed once by resolveDialPlan; the I/O loop in dialHoneyWithAuth then walks
// it without re-resolving anything.
type dialPlan struct {
	hops []hopPlan
	leaf hopPlan
	auth goph.Auth
}

// hostConfigResolver resolves an SSH alias to its config. Production passes
// lookupHostSSHConfig (which may shell out to `ssh -G`); tests inject a fake so
// resolveDialPlan's chain logic is exercised without a live network or ssh
// binary.
type hostConfigResolver func(alias, userOverride string) (*hostSSHConfig, error)

// resolveDialPlan resolves a target alias and its ProxyJump chain into a dialPlan.
// It calls resolve exactly once per host — the leaf and each hop — collecting
// identity files and building auth in the same pass, so a proxyjump dial no
// longer resolves each hop twice (once to gather identities, once to dial).
// overridePort, when in range, replaces the leaf port only. When exclusiveAuth is
// non-nil it is used verbatim and no identity files are gathered.
func resolveDialPlan(resolve hostConfigResolver, userOverride, hostAlias string, overridePort int, exclusiveAuth goph.Auth) (dialPlan, error) {
	leafCfg, err := resolve(hostAlias, userOverride)
	if err != nil {
		return dialPlan{}, err
	}
	final := leafCfg.resolved
	if overridePort > 0 && overridePort < 65536 {
		final.port = overridePort
	}

	jumps := parseProxyJumpChain(leafCfg.proxyJump)
	hops := make([]hopPlan, 0, len(jumps))
	idFiles := append([]string(nil), leafCfg.identityPaths...)
	for _, hopSpec := range jumps {
		explicitUser, hopHost, specPort, portFromSpec, perr := parseJumpSpec(hopSpec)
		if perr != nil {
			return dialPlan{}, perr
		}
		hopCfg, herr := resolve(hopHost, explicitUser)
		if herr != nil {
			return dialPlan{}, herr
		}
		res := hopCfg.resolved
		port := res.port
		if portFromSpec {
			port = specPort
		}
		hops = append(hops, hopPlan{host: res.host, port: port, user: res.user, cfg: hopCfg})
		idFiles = append(idFiles, hopCfg.identityPaths...)
	}

	auth := exclusiveAuth
	if auth == nil {
		auth, err = buildAuthWithIdentityFiles(idFiles)
		if err != nil {
			return dialPlan{}, err
		}
	}

	return dialPlan{
		hops: hops,
		leaf: hopPlan{host: final.host, port: final.port, user: final.user, cfg: leafCfg},
		auth: auth,
	}, nil
}

// finishSSHHandshake bounds the SSH handshake with sshHandshakeTimeout and
// returns the client. crypto/ssh's Dial applies ClientConfig.Timeout ONLY to the
// TCP connect (net.DialTimeout); the handshake that follows — banner exchange,
// key exchange, auth, and the host-key callback — runs with NO deadline. So a
// host that accepts the TCP connection but never drives the SSH handshake to
// completion (an overloaded sshd, MaxStartups throttling, a silent middlebox, a
// half-open cached path) blocks the caller forever. In a bounded parallel exec
// that is enough to wedge one worker and, through the pool's wg.Wait, stall the
// whole batch at ~N/M with no error and no timeout ever surfacing. Setting a
// deadline across NewClientConn — then clearing it on success so it never fires
// on the live session — makes a stuck handshake fail fast instead. The bound is
// sshHandshakeTimeout (generous), NOT the 20s TCP-connect timeout, which was too
// tight and tripped randomly on slow-but-working hosts.
//
// The deadline is best-effort: a raw TCP conn honours it, but an SSH-channel
// conn (a ProxyJump hop) does not support deadlines and returns an error from
// SetDeadline. In that case proceed without one — the bastion connection bounds
// liveness — rather than failing the dial.
func finishSSHHandshake(conn net.Conn, addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	deadlined := false
	if err := conn.SetDeadline(time.Now().Add(sshHandshakeTimeout)); err == nil {
		deadlined = true
	}
	ncc, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if deadlined {
		_ = conn.SetDeadline(time.Time{})
	}
	return ssh.NewClient(ncc, chans, reqs), nil
}

// sshDialDirect and sshDialVia are the crypto/ssh dial primitives used by the
// dial chain, behind package vars so tests can substitute a fake transport for
// the ProxyJump I/O loop. Production uses the real ones. Both bound the SSH
// handshake via finishSSHHandshake (see its doc for why Dial's own Timeout is
// insufficient).
var (
	sshDialDirect = func(addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
		conn, err := net.DialTimeout("tcp", addr, cfg.Timeout)
		if err != nil {
			return nil, err
		}
		return finishSSHHandshake(conn, addr, cfg)
	}
	sshDialVia = func(via *ssh.Client, addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
		rawConn, err := via.Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		return finishSSHHandshake(rawConn, addr, cfg)
	}
)
