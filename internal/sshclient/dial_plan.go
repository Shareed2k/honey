package sshclient

import (
	"net"
	"strconv"

	"github.com/melbahja/goph"
	"golang.org/x/crypto/ssh"
)

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

// sshDialDirect and sshDialVia are the crypto/ssh dial primitives used by the
// dial chain, behind package vars so tests can substitute a fake transport for
// the ProxyJump I/O loop. Production uses the real ones.
var (
	sshDialDirect = func(addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
		return ssh.Dial("tcp", addr, cfg)
	}
	sshDialVia = func(via *ssh.Client, addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
		rawConn, err := via.Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		ncc, chans, reqs, err := ssh.NewClientConn(rawConn, addr, cfg)
		if err != nil {
			_ = rawConn.Close()
			return nil, err
		}
		return ssh.NewClient(ncc, chans, reqs), nil
	}
)
