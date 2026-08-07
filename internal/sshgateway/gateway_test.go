package sshgateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"
	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/policy"
	"github.com/shareed2k/honey/internal/recordings"
)

// --- test doubles -----------------------------------------------------------

// memSink is an in-memory audit.Sink used to observe gateway decisions. It is
// mutex-guarded because Log runs on the gateway's session goroutine while the
// test asserts from another goroutine (the SSH channel that orders them is not
// tracked by the race detector).
type memSink struct {
	mu     sync.Mutex
	events []audit.Event
}

func (m *memSink) Log(_ context.Context, e audit.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, e)
	return nil
}
func (m *memSink) Close() error { return nil }

func (m *memSink) find(action string) (audit.Event, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.events {
		if e.Action == action {
			return e, true
		}
	}
	return audit.Event{}, false
}

func staticRecords(recs ...hosts.Record) func(context.Context) ([]hosts.Record, error) {
	return func(context.Context) ([]hosts.Record, error) { return recs, nil }
}

// --- key / cert helpers ------------------------------------------------------

func newEd25519Signer(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	return signer
}

// signedCertAuth builds a client auth method presenting a user certificate
// signed by ca with the given principal, key id, and expiry.
func signedCertAuth(t *testing.T, ca ssh.Signer, principal, keyID string, validBefore time.Time) ssh.AuthMethod {
	t.Helper()
	userSigner := newEd25519Signer(t)
	cert := &ssh.Certificate{
		Key:             userSigner.PublicKey(),
		Serial:          1,
		CertType:        ssh.UserCert,
		KeyId:           keyID,
		ValidPrincipals: []string{principal},
		ValidAfter:      uint64(time.Now().Add(-time.Minute).Unix()),
		ValidBefore:     uint64(validBefore.Unix()),
		Permissions:     ssh.Permissions{Extensions: map[string]string{"permit-pty": ""}},
	}
	if err := cert.SignCert(rand.Reader, ca); err != nil {
		t.Fatalf("sign cert: %v", err)
	}
	cs, err := ssh.NewCertSigner(cert, userSigner)
	if err != nil {
		t.Fatalf("cert signer: %v", err)
	}
	return ssh.PublicKeys(cs)
}

// --- target sshd fixture -----------------------------------------------------

// targetKey generates an RSA identity, writes the private key to a temp file
// (the form honey's dial stack loads), and returns the file path plus the
// authorized public key.
func targetKey(t *testing.T) (identityPath string, pub ssh.PublicKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	dir := t.TempDir()
	identityPath = filepath.Join(dir, "id_rsa")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(identityPath, pemBytes, 0o600); err != nil {
		t.Fatalf("write identity: %v", err)
	}
	return identityPath, signer.PublicKey()
}

// startTargetSSHD starts a minimal in-process sshd that authenticates authKey
// and serves session channels: it echoes an interactive shell and runs a couple
// of ad-hoc commands (echo, cat). Returns the listen port and a stop func.
func startTargetSSHD(t *testing.T, authKey ssh.PublicKey) (port int, stop func()) {
	t.Helper()
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(key.Marshal(), authKey.Marshal()) {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("unauthorized key")
		},
	}
	cfg.AddHostKey(newEd25519Signer(t))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen target: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			raw, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go serveTargetConn(raw, cfg)
		}
	}()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(portStr)
	return p, func() {
		_ = ln.Close()
		<-done
	}
}

func serveTargetConn(raw net.Conn, cfg *ssh.ServerConfig) {
	sc, chans, reqs, err := ssh.NewServerConn(raw, cfg)
	if err != nil {
		_ = raw.Close()
		return
	}
	defer func() { _ = sc.Close() }()
	go ssh.DiscardRequests(reqs)

	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			_ = newCh.Reject(ssh.UnknownChannelType, "unsupported")
			continue
		}
		go serveTargetSession(newCh)
	}
}

func serveTargetSession(newCh ssh.NewChannel) {
	ch, reqs, err := newCh.Accept()
	if err != nil {
		return
	}
	for req := range reqs {
		switch req.Type {
		case "pty-req", "env", "window-change":
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
		case "shell":
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
			// Interactive: echo stdin to stdout until the client closes stdin.
			_, _ = io.Copy(ch, ch)
			sendTargetExit(ch, 0)
			_ = ch.Close()
			return
		case "exec":
			var p struct{ Command string }
			_ = ssh.Unmarshal(req.Payload, &p)
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
			code := runTargetCommand(ch, p.Command)
			sendTargetExit(ch, code)
			_ = ch.Close()
			return
		default:
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}
	_ = ch.Close()
}

// runTargetCommand implements just enough of a remote shell for the tests.
func runTargetCommand(ch ssh.Channel, command string) int {
	// The interactive path (ui.RunSSHInteractiveStreams) always sends an
	// env-export prefix followed by `exec "${SHELL:-sh}" ...`; treat that as an
	// interactive echo shell.
	if strings.Contains(command, "${SHELL") {
		_, _ = io.Copy(ch, ch)
		return 0
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return 0
	}
	switch fields[0] {
	case "echo":
		_, _ = fmt.Fprintln(ch, strings.Join(fields[1:], " "))
		return 0
	case "cat":
		_, _ = io.Copy(ch, ch)
		return 0
	case "false":
		return 3
	default:
		_, _ = fmt.Fprintf(ch.Stderr(), "unknown command: %s\n", fields[0])
		return 127
	}
}

func sendTargetExit(ch ssh.Channel, code int) {
	_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)}))
}

// --- gateway harness ---------------------------------------------------------

func startGateway(t *testing.T, opts Options) (addr string, stop func()) {
	t.Helper()
	if opts.HostKey == nil {
		opts.HostKey = newEd25519Signer(t)
	}
	if opts.ListenAddr == "" {
		opts.ListenAddr = "127.0.0.1:0"
	}
	srv, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Start(ctx) }()
	select {
	case <-srv.Ready():
	case err := <-done:
		cancel()
		t.Fatalf("gateway exited early: %v", err)
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("gateway did not become ready")
	}
	return srv.Addr(), func() {
		cancel()
		<-done
	}
}

// dialGateway connects an ssh client to the gateway with the given auth.
func dialGateway(t *testing.T, addr, user string, auth ssh.AuthMethod) (*ssh.Client, error) {
	t.Helper()
	return ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{auth},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
}

// sandboxSSHEnv points honey's dial stack at a temp HOME and disables shelling
// out to `ssh -G`, so target dials are hermetic and deterministic.
func sandboxSSHEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HONEY_SSH_OPENSSH_G", "0")
}

func targetRecord(name, identity string, port int) hosts.Record {
	rec := hosts.Record{Provider: "local", Name: name, PrimaryIP: "127.0.0.1"}
	rec = hosts.CloneWithMetaSSHPort(rec, port)
	rec = hosts.CloneWithMetaSSHIdentityFile(rec, identity)
	return rec
}

// --- tests -------------------------------------------------------------------

func TestGateway_CertAuth(t *testing.T) {
	ca := newEd25519Signer(t)
	otherCA := newEd25519Signer(t)
	caPub := ca.PublicKey()

	addr, stop := startGateway(t, Options{
		TrustedCAs: []ssh.PublicKey{caPub},
		Records:    staticRecords(),
	})
	defer stop()

	unsignedSigner := newEd25519Signer(t)

	tests := []struct {
		name    string
		user    string
		auth    ssh.AuthMethod
		wantErr bool
	}{
		{name: "ca-signed cert accepted", user: "alice", auth: signedCertAuth(t, ca, "alice", "alice@corp", time.Now().Add(time.Hour))},
		{name: "unsigned plain key rejected", user: "alice", auth: ssh.PublicKeys(unsignedSigner), wantErr: true},
		{name: "wrong-CA cert rejected", user: "alice", auth: signedCertAuth(t, otherCA, "alice", "alice@corp", time.Now().Add(time.Hour)), wantErr: true},
		{name: "expired cert rejected", user: "alice", auth: signedCertAuth(t, ca, "alice", "alice@corp", time.Now().Add(-time.Hour)), wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, err := dialGateway(t, addr, tc.user, tc.auth)
			if tc.wantErr {
				if err == nil {
					_ = client.Close()
					t.Fatalf("expected auth failure, got success")
				}
				return
			}
			if err != nil {
				t.Fatalf("expected auth success, got %v", err)
			}
			_ = client.Close()
		})
	}
}

func TestGateway_ExecAndRouting(t *testing.T) {
	sandboxSSHEnv(t)

	identity, pub := targetKey(t)
	port, stopTarget := startTargetSSHD(t, pub)
	defer stopTarget()

	ca := newEd25519Signer(t)
	sink := &memSink{}
	rec := targetRecord("web1", identity, port)

	addr, stopGW := startGateway(t, Options{
		TrustedCAs: []ssh.PublicKey{ca.PublicKey()},
		AuditSink:  sink,
		Records:    staticRecords(rec),
	})
	defer stopGW()

	client, err := dialGateway(t, addr, "bob", signedCertAuth(t, ca, "bob", "bob@corp", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	t.Run("ad-hoc exec stdout and exit status", func(t *testing.T) {
		sess, err := client.NewSession()
		if err != nil {
			t.Fatalf("session: %v", err)
		}
		defer func() { _ = sess.Close() }()
		out, err := sess.Output("web1 echo hello")
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if strings.TrimSpace(string(out)) != "hello" {
			t.Fatalf("stdout = %q, want hello", out)
		}
	})

	t.Run("nonzero exit status propagates", func(t *testing.T) {
		sess, err := client.NewSession()
		if err != nil {
			t.Fatalf("session: %v", err)
		}
		defer func() { _ = sess.Close() }()
		err = sess.Run("web1 false")
		var ee *ssh.ExitError
		if err == nil {
			t.Fatalf("expected nonzero exit")
		}
		if !errors.As(err, &ee) || ee.ExitStatus() != 3 {
			t.Fatalf("exit status = %v, want 3", err)
		}
	})

	t.Run("stdin pipe reaches command", func(t *testing.T) {
		sess, err := client.NewSession()
		if err != nil {
			t.Fatalf("session: %v", err)
		}
		defer func() { _ = sess.Close() }()
		sess.Stdin = strings.NewReader("piped-input")
		out, err := sess.Output("web1 cat")
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if string(out) != "piped-input" {
			t.Fatalf("stdout = %q, want piped-input", out)
		}
	})

	t.Run("unknown resource clean error", func(t *testing.T) {
		sess, err := client.NewSession()
		if err != nil {
			t.Fatalf("session: %v", err)
		}
		defer func() { _ = sess.Close() }()
		var stderr bytes.Buffer
		sess.Stderr = &stderr
		err = sess.Run("nope echo hi")
		if err == nil {
			t.Fatalf("expected error for unknown resource")
		}
		if !strings.Contains(stderr.String(), "no search result") {
			t.Fatalf("stderr = %q, want resolve error", stderr.String())
		}
	})

	t.Run("principal maps to actor in audit", func(t *testing.T) {
		e, ok := sink.find("command_exec")
		if !ok {
			t.Fatalf("no command_exec audit event")
		}
		if e.Actor != "bob" {
			t.Fatalf("actor = %q, want bob (cert principal -> actor)", e.Actor)
		}
		if e.Source != "ssh-gateway" {
			t.Fatalf("source = %q, want ssh-gateway", e.Source)
		}
	})
}

func TestGateway_InteractiveRecording(t *testing.T) {
	sandboxSSHEnv(t)

	identity, pub := targetKey(t)
	port, stopTarget := startTargetSSHD(t, pub)
	defer stopTarget()

	recordDir := t.TempDir()
	ca := newEd25519Signer(t)
	rec := targetRecord("web1", identity, port)

	addr, stopGW := startGateway(t, Options{
		TrustedCAs: []ssh.PublicKey{ca.PublicKey()},
		RecordDir:  recordDir,
		Records:    staticRecords(rec),
	})
	defer stopGW()

	client, err := dialGateway(t, addr, "alice", signedCertAuth(t, ca, "alice", "alice@corp", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	sess, err := client.NewSession()
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	defer func() { _ = sess.Close() }()

	modes := ssh.TerminalModes{ssh.ECHO: 1}
	if err := sess.RequestPty("xterm-256color", 24, 80, modes); err != nil {
		t.Fatalf("pty: %v", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	var out bytes.Buffer
	sess.Stdout = &out
	// Interactive: resource only + pty -> shell proxy.
	if err := sess.Start("web1"); err != nil {
		t.Fatalf("start: %v", err)
	}
	_, _ = stdin.Write([]byte("hello\n"))
	_ = stdin.Close()
	if err := sess.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}

	base := findRecording(t, recordDir)
	events, err := recordings.LoadEvents(recordDir, base)
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	if len(events) == 0 || events[0].Type != "open" {
		t.Fatalf("first event = %+v, want type open", events)
	}
}

func TestGateway_OPADenyBlocksExec(t *testing.T) {
	const src = `package honey
import rego.v1
default allow := true
default deny_reason := ""
allow := false if input.action == "command_exec"
deny_reason := "command_exec blocked by test policy" if input.action == "command_exec"
`
	enf, err := policy.NewFromSource(context.Background(), "deny.rego", src)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}

	ca := newEd25519Signer(t)
	sink := &memSink{}
	// A resolvable resource is enough; the deny fires before any target dial.
	rec := hosts.Record{Provider: "local", Name: "web1", PrimaryIP: "127.0.0.1"}

	addr, stopGW := startGateway(t, Options{
		TrustedCAs: []ssh.PublicKey{ca.PublicKey()},
		Enforcer:   enf,
		AuditSink:  sink,
		Records:    staticRecords(rec),
	})
	defer stopGW()

	client, err := dialGateway(t, addr, "alice", signedCertAuth(t, ca, "alice", "alice@corp", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	sess, err := client.NewSession()
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	defer func() { _ = sess.Close() }()
	var stderr bytes.Buffer
	sess.Stderr = &stderr
	err = sess.Run("web1 echo hi")
	if err == nil {
		t.Fatalf("expected denied exec to fail")
	}
	if !strings.Contains(stderr.String(), "command_exec blocked by test policy") {
		t.Fatalf("stderr = %q, want deny reason", stderr.String())
	}
	e, ok := sink.find("command_exec")
	if !ok || e.Decision != "deny" {
		t.Fatalf("audit command_exec = %+v, want deny", e)
	}
}

func TestGateway_NoGoroutineLeaks(t *testing.T) {
	// engine's GlobalTunnelPool sweepLoop is a process-lifetime singleton started
	// via a package-level var; it is not a per-session leak.
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("github.com/shareed2k/honey/internal/engine.(*GlobalTunnelPool).sweepLoop"),
	)

	sandboxSSHEnv(t)

	identity, pub := targetKey(t)
	port, stopTarget := startTargetSSHD(t, pub)
	defer stopTarget()

	ca := newEd25519Signer(t)
	rec := targetRecord("web1", identity, port)

	addr, stopGW := startGateway(t, Options{
		TrustedCAs: []ssh.PublicKey{ca.PublicKey()},
		Records:    staticRecords(rec),
	})
	defer stopGW()

	client, err := dialGateway(t, addr, "alice", signedCertAuth(t, ca, "alice", "alice@corp", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	sess, err := client.NewSession()
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	out, err := sess.Output("web1 echo done")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.TrimSpace(string(out)) != "done" {
		t.Fatalf("stdout = %q, want done", out)
	}
	_ = sess.Close()
	_ = client.Close()
}

func TestTargetUser(t *testing.T) {
	tests := []struct {
		name, metaUser, defUser, actor, want string
	}{
		{"meta ssh_user wins", "deploy", "ops", "alice", "deploy"},
		{"default when no meta", "", "ops", "alice", "ops"},
		{"actor last-resort fallback", "", "", "alice", "alice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{opts: Options{DefaultSSHUser: tt.defUser}}
			rec := hosts.Record{}
			if tt.metaUser != "" {
				rec.Meta = map[string]string{"ssh_user": tt.metaUser}
			}
			if got := s.targetUser(rec, tt.actor); got != tt.want {
				t.Errorf("targetUser = %q, want %q", got, tt.want)
			}
		})
	}
}

// findRecording returns the single .hrec.jsonl base name in dir.
func findRecording(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read record dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".hrec.jsonl") {
			return e.Name()
		}
	}
	t.Fatalf("no recording found in %s", dir)
	return ""
}
