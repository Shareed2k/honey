package sshclient

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	sshagent "github.com/xanzy/ssh-agent"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/sync/semaphore"
)

// agentOpTimeout bounds a single ssh-agent operation. x/crypto/ssh/agent has no
// per-request timeout, so without this a stuck agent blocks a dialer forever.
const agentOpTimeout = 20 * time.Second

// defaultAgentConcurrency bounds concurrent ssh-agent operations.
//
// Every dial authenticates with keys the agent holds, so a large parallel exec
// hammers the one agent. Real agents — macOS launchd, and especially GUI/hardware
// agents (1Password, Secretive, gpg-agent, a token) — tolerate only a handful of
// concurrent connections; past that they stop answering, and because
// x/crypto/ssh/agent has no timeout, every dialer then blocks forever in an agent
// read and the whole batch stalls silently at ~N/M with no error and no timeout
// (observed on this setup: a hard wedge once ~6 agent connections were open at
// once). Keeping concurrency well under that — and holding each agent connection
// only for the signing op, not the whole SSH handshake — keeps the agent
// responsive. Tune with HONEY_SSH_AGENT_CONCURRENCY.
const defaultAgentConcurrency = 4

var agentSem = semaphore.NewWeighted(agentConcurrency())

func agentConcurrency() int64 {
	if v := strings.TrimSpace(os.Getenv("HONEY_SSH_AGENT_CONCURRENCY")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return int64(n)
		}
	}
	return defaultAgentConcurrency
}

// withAgentConn runs fn against a short-lived ssh-agent connection, bounded by
// the global concurrency semaphore and a per-op timeout, and always closes the
// connection so agent connections never accumulate and never exceed the limit.
//
// Acquire blocks rather than failing "busy": a healthy agent frees slots in
// milliseconds, and the op timeout frees a slot held by a genuinely stuck agent,
// so the batch keeps moving instead of wedging. A fresh connection per op (not a
// reused one) is deliberate: some agents drop a socket after one request, which
// wedges every later op on a reused connection.
func withAgentConn(fn func(agent.ExtendedAgent) error) error {
	_ = agentSem.Acquire(context.Background(), 1) // context.Background never errors
	defer agentSem.Release(1)

	_, conn, err := sshagent.New()
	if err != nil {
		return fmt.Errorf("ssh-agent: %w", err)
	}
	var once sync.Once
	closeConn := func() { once.Do(func() { _ = conn.Close() }) }
	defer closeConn()

	done := make(chan error, 1)
	go func() { done <- fn(agent.NewClient(conn)) }()

	timer := time.NewTimer(agentOpTimeout)
	defer timer.Stop()
	select {
	case e := <-done:
		return e
	case <-timer.C:
		closeConn() // unblock a stuck agent read so the goroutine can exit
		return fmt.Errorf("ssh-agent operation timed out after %s", agentOpTimeout)
	}
}

var (
	agentAvailOnce sync.Once
	agentAvailable bool
)

// hasSSHAgent reports whether an ssh-agent is reachable (probed once).
func hasSSHAgent() bool {
	agentAvailOnce.Do(func() {
		if _, conn, err := sshagent.New(); err == nil {
			_ = conn.Close()
			agentAvailable = true
		}
	})
	return agentAvailable
}

var (
	agentKeysOnce sync.Once
	agentKeys     []*agent.Key
	agentKeysErr  error
)

// cachedAgentKeys lists the agent's public keys once and reuses them for every
// dial. Without this, honey issues a redundant agent List per host — hundreds of
// extra agent round-trips at batch scale. Signing still goes to the agent per
// auth (a signature can't be cached).
func cachedAgentKeys() ([]*agent.Key, error) {
	agentKeysOnce.Do(func() {
		agentKeysErr = withAgentConn(func(a agent.ExtendedAgent) error {
			k, e := a.List()
			agentKeys = k
			return e
		})
	})
	return agentKeys, agentKeysErr
}

// agentSigners returns one bounded, self-closing signer per cached agent key.
// Called by ssh.PublicKeysCallback during the handshake.
func agentSigners() ([]ssh.Signer, error) {
	keys, err := cachedAgentKeys()
	if err != nil {
		return nil, err
	}
	signers := make([]ssh.Signer, 0, len(keys))
	for _, k := range keys {
		signers = append(signers, &honeyAgentSigner{pub: k})
	}
	return signers, nil
}

// honeyAgentSigner is an ssh.Signer / ssh.AlgorithmSigner that signs through a
// short-lived, bounded agent connection. Implementing AlgorithmSigner is required
// so RSA keys use rsa-sha2-256/512 — modern servers reject the SHA-1 "ssh-rsa"
// default a plain ssh.Signer would produce.
type honeyAgentSigner struct {
	pub *agent.Key
}

func (s *honeyAgentSigner) PublicKey() ssh.PublicKey { return s.pub }

func (s *honeyAgentSigner) Sign(_ io.Reader, data []byte) (*ssh.Signature, error) {
	return s.SignWithAlgorithm(nil, data, "")
}

func (s *honeyAgentSigner) SignWithAlgorithm(_ io.Reader, data []byte, algorithm string) (*ssh.Signature, error) {
	var flags agent.SignatureFlags
	switch algorithm {
	case ssh.KeyAlgoRSASHA256:
		flags = agent.SignatureFlagRsaSha256
	case ssh.KeyAlgoRSASHA512:
		flags = agent.SignatureFlagRsaSha512
	}
	var sig *ssh.Signature
	if err := withAgentConn(func(a agent.ExtendedAgent) error {
		var e error
		if flags == 0 {
			sig, e = a.Sign(s.pub, data)
		} else {
			sig, e = a.SignWithFlags(s.pub, data, flags)
		}
		return e
	}); err != nil {
		return nil, err
	}
	if sig == nil {
		return nil, fmt.Errorf("ssh-agent returned no signature")
	}
	return sig, nil
}

var (
	_ ssh.Signer          = (*honeyAgentSigner)(nil)
	_ ssh.AlgorithmSigner = (*honeyAgentSigner)(nil)
)
