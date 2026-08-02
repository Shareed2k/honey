package sshclient

import (
	"fmt"
	"io"
	"sync"
	"time"

	sshagent "github.com/xanzy/ssh-agent"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// agentOpTimeout bounds a single ssh-agent request. A real agent (macOS launchd,
// 1Password, gpg-agent, a hardware token) can stall or rate-limit under load;
// x/crypto/ssh/agent itself has no per-request timeout, so a bounded op turns a
// stuck agent into a failed dial instead of a permanently wedged worker.
const agentOpTimeout = 15 * time.Second

// honeyAgent is a process-wide, serialized proxy over the ssh-agent.
//
// The default path (goph.UseAgent) opens a FRESH agent socket on every dial and
// never closes it, so a parallel exec across hundreds of hosts drives hundreds
// of concurrent connections and sign requests at the one agent. Real agents
// serialize internally and cap connections, so they wedge under that storm — and
// because x/crypto/ssh/agent has no per-request timeout, every dialer then blocks
// forever in an agent read and the whole batch stalls with no error surfacing
// (observed: ~236/N hosts done, ~300 goroutines stuck in x/crypto/ssh/agent).
//
// honeyAgent holds ONE connection and runs every request through a single owner
// goroutine, so the agent sees one operation at a time (no connection storm, no
// leaked sockets), and bounds each operation with agentOpTimeout so a stuck agent
// fails the dial fast instead of hanging the worker (which also lets a user Stop
// take effect). A single genuinely-stuck request blocks only the owner goroutine;
// callers time out, and once the queue fills, submitters fail fast too.
type honeyAgent struct {
	reqs chan agentJob
}

type agentJob struct {
	fn    func(agent.ExtendedAgent) (any, error)
	reply chan agentResult
}

type agentResult struct {
	val any
	err error
}

var (
	honeyAgentOnce sync.Once
	honeyAgentInst *honeyAgent
	honeyAgentErr  error
)

// sharedHoneyAgent lazily dials the ssh-agent once and starts its owner
// goroutine. All dials in the process share the returned proxy.
//
// The connection lives for the process lifetime; if the agent is restarted mid-
// run, agent auth fails until honey restarts (a fresh key-file dial still works).
func sharedHoneyAgent() (*honeyAgent, error) {
	honeyAgentOnce.Do(func() {
		// xanzy/ssh-agent resolves SSH_AUTH_SOCK (and a Windows named pipe) and
		// dials the agent, keeping the env-var-to-socket handling behind a library
		// boundary. Wrap the returned conn in agent.NewClient for the ExtendedAgent
		// API (SignWithFlags) that honeyAgentSigner needs.
		_, conn, err := sshagent.New()
		if err != nil {
			honeyAgentErr = fmt.Errorf("ssh-agent: %w", err)
			return
		}
		ha := &honeyAgent{reqs: make(chan agentJob, 512)}
		go ha.serve(agent.NewClient(conn))
		honeyAgentInst = ha
	})
	return honeyAgentInst, honeyAgentErr
}

// serve owns the single agent connection and processes one request at a time.
func (h *honeyAgent) serve(client agent.ExtendedAgent) {
	for job := range h.reqs {
		val, err := job.fn(client)
		job.reply <- agentResult{val: val, err: err}
	}
}

// do submits one agent operation and waits up to agentOpTimeout for both a queue
// slot and a reply. reply is buffered so the owner goroutine never blocks sending
// even if this caller has already timed out.
func (h *honeyAgent) do(fn func(agent.ExtendedAgent) (any, error)) (any, error) {
	reply := make(chan agentResult, 1)
	submit := time.NewTimer(agentOpTimeout)
	defer submit.Stop()
	select {
	case h.reqs <- agentJob{fn: fn, reply: reply}:
	case <-submit.C:
		return nil, fmt.Errorf("ssh-agent busy: request queue full for %s", agentOpTimeout)
	}
	wait := time.NewTimer(agentOpTimeout)
	defer wait.Stop()
	select {
	case r := <-reply:
		return r.val, r.err
	case <-wait.C:
		return nil, fmt.Errorf("ssh-agent request timed out after %s", agentOpTimeout)
	}
}

// signers lists the agent's keys and wraps each in a serialized, timeout-bounded
// signer.
func (h *honeyAgent) signers() ([]ssh.Signer, error) {
	v, err := h.do(func(a agent.ExtendedAgent) (any, error) {
		return a.List()
	})
	if err != nil {
		return nil, err
	}
	keys, _ := v.([]*agent.Key)
	signers := make([]ssh.Signer, 0, len(keys))
	for _, k := range keys {
		signers = append(signers, &honeyAgentSigner{agent: h, pub: k})
	}
	return signers, nil
}

// honeyAgentSigner is an ssh.Signer / ssh.AlgorithmSigner that signs through the
// serialized honeyAgent. Implementing AlgorithmSigner is required so RSA keys use
// rsa-sha2-256/512 — modern servers reject the SHA-1 "ssh-rsa" default that a
// plain ssh.Signer would produce.
type honeyAgentSigner struct {
	agent *honeyAgent
	pub   *agent.Key
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
	v, err := s.agent.do(func(a agent.ExtendedAgent) (any, error) {
		if flags == 0 {
			return a.Sign(s.pub, data)
		}
		return a.SignWithFlags(s.pub, data, flags)
	})
	if err != nil {
		return nil, err
	}
	sig, _ := v.(*ssh.Signature)
	if sig == nil {
		return nil, fmt.Errorf("ssh-agent returned no signature")
	}
	return sig, nil
}

var (
	_ ssh.Signer          = (*honeyAgentSigner)(nil)
	_ ssh.AlgorithmSigner = (*honeyAgentSigner)(nil)
)

// honeyAgentAuthMethod returns an ssh.AuthMethod backed by the shared, serialized
// agent, or (nil, false) when no agent is available.
func honeyAgentAuthMethod() (ssh.AuthMethod, bool) {
	ha, err := sharedHoneyAgent()
	if err != nil || ha == nil {
		return nil, false
	}
	return ssh.PublicKeysCallback(ha.signers), true
}
