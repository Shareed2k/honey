package sshgateway

import (
	"bytes"
	"context"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"
	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
)

// --- Phase F fakes: a hostexec.Registry/Executor test double -----------------
//
// The gateway routes docker/k8s/mesh targets through the hostexec seam. Its
// whole test surface is the hostexec.Registry interface, so these fakes replace
// a real docker/k8s daemon: they record which seam method the gateway invoked
// and produce deterministic output.

// fakeRegistry resolves every record to a single fixed executor.
type fakeRegistry struct{ ex hostexec.Executor }

func (f fakeRegistry) ForRecord(hosts.Record) hostexec.Executor { return f.ex }
func (fakeRegistry) Reconfigure(*config.File)                   {}
func (fakeRegistry) RunSSHTunnel(context.Context, string, string, int, string, io.Writer) error {
	return nil
}
func (fakeRegistry) BorrowSSH(string, hosts.Record) (any, bool) { return nil, false }

var _ hostexec.Registry = fakeRegistry{}

// fakeExec is a hostexec.Executor + InteractiveStreamer + ProxyExecutor test
// double. It models a provider shell/exec/tunnel over caller-provided streams
// without any real backend.
type fakeExec struct {
	mu             sync.Mutex
	interactiveHit bool
	banner         string // written to stdout at the start of an interactive session
	proxy          bool   // when true IsProxy() reports true (the mesh case)
}

var (
	_ hostexec.Executor            = (*fakeExec)(nil)
	_ hostexec.InteractiveStreamer = (*fakeExec)(nil)
	_ hostexec.ProxyExecutor       = (*fakeExec)(nil)
)

func (f *fakeExec) sawInteractive() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.interactiveHit
}

// RunInteractiveStreams writes an optional banner, then copies stdin to stdout
// until stdin EOF — modeling a provider shell over the caller's streams. It
// returns when the client closes stdin, so the gateway's stream teardown ends
// it. resize is intentionally not read: the gateway's forwarder writes it
// non-blocking (dropping when full), and the channel is never closed, so
// draining it would leak a goroutine.
func (f *fakeExec) RunInteractiveStreams(_ context.Context, _ string, _ hosts.Record, stdin io.Reader, stdout io.Writer, _, _ int, _ <-chan [2]int) error {
	f.mu.Lock()
	f.interactiveHit = true
	f.mu.Unlock()
	if f.banner != "" {
		_, _ = io.WriteString(stdout, f.banner)
	}
	_, _ = io.Copy(stdout, stdin)
	return nil
}

func (f *fakeExec) Dial(string, hosts.Record) (hostexec.HostClient, error) {
	return fakeHostClient{}, nil
}

// DialUpstream returns the gateway's end of a net.Pipe wired to a bounded echo
// goroutine on the far end; the goroutine exits when the gateway closes its end
// during teardown.
func (f *fakeExec) DialUpstream(context.Context, string, hosts.Record, string) (net.Conn, error) {
	gwEnd, echoEnd := net.Pipe()
	go func() {
		defer func() { _ = echoEnd.Close() }()
		_, _ = io.Copy(echoEnd, echoEnd)
	}()
	return gwEnd, nil
}

func (f *fakeExec) IsProxy() bool { return f.proxy }

func (f *fakeExec) RunInteractive(string, hosts.Record) error { return nil }
func (f *fakeExec) RunTunnel(context.Context, string, hosts.Record, string, io.Writer) error {
	return nil
}

// fakeHostClient is a no-op HostClient whose RunWithStreams echoes the command
// so an exec route can be asserted; every other method is an inert stub.
type fakeHostClient struct{}

var _ hostexec.HostClient = fakeHostClient{}

func (fakeHostClient) Run(string) ([]byte, error) { return nil, nil }
func (fakeHostClient) RunWithStreams(cmd string, _ io.Reader, stdout, _ io.Writer) error {
	_, _ = io.WriteString(stdout, "ran:"+cmd+"\n")
	return nil
}
func (fakeHostClient) Upload(string, string) error                              { return nil }
func (fakeHostClient) Download(string, string) error                            { return nil }
func (fakeHostClient) ListRemoteDir(string) ([]hostexec.RemoteFileEntry, error) { return nil, nil }
func (fakeHostClient) StatRemote(string) (hostexec.RemoteFileEntry, error) {
	return hostexec.RemoteFileEntry{}, nil
}
func (fakeHostClient) MkdirAllRemote(string) error     { return nil }
func (fakeHostClient) RemoveRemote(string, bool) error { return nil }
func (fakeHostClient) StartLocalForward(context.Context, string, int, string, int) (string, int, func(), error) {
	return "", 0, func() {}, nil
}

func (fakeHostClient) StartRemoteForward(context.Context, string, int, string, int) (string, func(), error) {
	return "", func() {}, nil
}

func (fakeHostClient) StartDynamicForward(context.Context, string, int) (string, int, func(), error) {
	return "", 0, func() {}, nil
}

func (fakeHostClient) StartUDPRelay(context.Context, string, int, string, int, bool) (string, int, func(), error) {
	return "", 0, func() {}, nil
}

func (fakeHostClient) StartTunForward(context.Context, string, string, int, int, int) (string, func(), error) {
	return "", func() {}, nil
}

func (fakeHostClient) StartLocalSocketForward(context.Context, string, string) (string, func(), error) {
	return "", func() {}, nil
}

func (fakeHostClient) StartLocalTCPToSocketForward(context.Context, string, int, string) (string, int, func(), error) {
	return "", 0, func() {}, nil
}
func (fakeHostClient) Close() error { return nil }

// containerRecord builds a docker-container record named "cont1" (IsDocker()
// true) so the gateway routes it through the provider seam rather than a raw SSH
// leaf. It carries a PrimaryIP only because the gateway's name resolver
// (cuetry.ResolveHostFromRecords) requires one for a by-name match — routing
// keys off IsDocker(), not the IP.
func containerRecord() hosts.Record {
	return hosts.Record{
		Provider:  "docker",
		Name:      "cont1",
		PrimaryIP: "10.88.0.7",
		Meta:      map[string]string{"kind": "container", "container_id": "abc123"},
	}
}

// ignoreTunnelPoolSweep is the process-lifetime singleton the existing gateway
// leak tests also ignore (see gateway_test.go / directtcpip_test.go).
func ignoreTunnelPoolSweep() goleak.Option {
	return goleak.IgnoreTopFunction("github.com/shareed2k/honey/internal/engine.(*GlobalTunnelPool).sweepLoop")
}

// --- tests -------------------------------------------------------------------

// TestGateway_ProviderInteractive proves an interactive shell to a native
// (docker container) record is served by the provider executor's
// RunInteractiveStreams — not the SSH leaf — and is still recorded.
func TestGateway_ProviderInteractive(t *testing.T) {
	defer goleak.VerifyNone(t, ignoreTunnelPoolSweep())

	recordDir := t.TempDir()
	ca := newEd25519Signer(t)
	fe := &fakeExec{banner: "provider-shell-ready\n"}
	rec := containerRecord()

	addr, stopGW := startGateway(t, Options{
		TrustedCAs:   []ssh.PublicKey{ca.PublicKey()},
		RecordDir:    recordDir,
		Records:      staticRecords(rec),
		ExecRegistry: fakeRegistry{ex: fe},
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

	if err := sess.RequestPty("xterm-256color", 24, 80, ssh.TerminalModes{ssh.ECHO: 1}); err != nil {
		t.Fatalf("pty: %v", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	var out bytes.Buffer
	sess.Stdout = &out
	// Interactive: resource only + pty -> shell proxy.
	if err := sess.Start("cont1"); err != nil {
		t.Fatalf("start: %v", err)
	}
	_, _ = stdin.Write([]byte("hi\n"))
	_ = stdin.Close()
	if err := sess.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}

	if !fe.sawInteractive() {
		t.Fatalf("provider RunInteractiveStreams was not invoked")
	}
	if !strings.Contains(out.String(), "provider-shell-ready") {
		t.Fatalf("stdout = %q, want provider banner", out.String())
	}
	_ = findRecording(t, recordDir) // a recording was written (fatals if absent)
}

// TestGateway_ProviderExec proves an ad-hoc command on a native record routes to
// the provider HostClient.RunWithStreams, with stdout and a zero exit status.
func TestGateway_ProviderExec(t *testing.T) {
	defer goleak.VerifyNone(t, ignoreTunnelPoolSweep())

	ca := newEd25519Signer(t)
	sink := &memSink{}
	fe := &fakeExec{}
	rec := containerRecord()

	addr, stopGW := startGateway(t, Options{
		TrustedCAs:   []ssh.PublicKey{ca.PublicKey()},
		AuditSink:    sink,
		Records:      staticRecords(rec),
		ExecRegistry: fakeRegistry{ex: fe},
	})
	defer stopGW()

	client, err := dialGateway(t, addr, "bob", signedCertAuth(t, ca, "bob", "bob@corp", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	sess, err := client.NewSession()
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	defer func() { _ = sess.Close() }()

	out, err := sess.Output("cont1 echo hi")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "ran:") || !strings.Contains(got, "echo hi") {
		t.Fatalf("stdout = %q, want to contain ran:echo hi", got)
	}
	e, ok := sink.find("command_exit")
	if !ok || e.ExitCode == nil || *e.ExitCode != 0 {
		t.Fatalf("command_exit audit = %+v, want exit 0", e)
	}
}

// TestGateway_ProviderExecMeshProxy proves a mesh-proxied record (not docker/
// k8s, native only because the executor forwards it) also routes exec through
// the seam via hostexec.IsProxy.
func TestGateway_ProviderExecMeshProxy(t *testing.T) {
	defer goleak.VerifyNone(t, ignoreTunnelPoolSweep())

	ca := newEd25519Signer(t)
	fe := &fakeExec{proxy: true}
	rec := hosts.Record{Provider: "gcp", Name: "mesh1", PrimaryIP: "10.0.0.9"}

	addr, stopGW := startGateway(t, Options{
		TrustedCAs:   []ssh.PublicKey{ca.PublicKey()},
		Records:      staticRecords(rec),
		ExecRegistry: fakeRegistry{ex: fe},
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

	out, err := sess.Output("mesh1 uptime")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(string(out), "ran:uptime") {
		t.Fatalf("stdout = %q, want ran:uptime", out)
	}
}

// TestGateway_ProviderDirectTCPIP proves `ssh -L` to a native record dials the
// provider DialUpstream and round-trips bytes through the returned conn.
func TestGateway_ProviderDirectTCPIP(t *testing.T) {
	defer goleak.VerifyNone(t, ignoreTunnelPoolSweep())

	ca := newEd25519Signer(t)
	sink := &memSink{}
	fe := &fakeExec{}
	rec := containerRecord()

	addr, stopGW := startGateway(t, Options{
		TrustedCAs:   []ssh.PublicKey{ca.PublicKey()},
		AuditSink:    sink,
		Records:      staticRecords(rec),
		ExecRegistry: fakeRegistry{ex: fe},
	})
	defer stopGW()

	client, err := dialGateway(t, addr, "alice", signedCertAuth(t, ca, "alice", "alice@corp", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	fwd, err := client.Dial("tcp", net.JoinHostPort("cont1", "5432"))
	if err != nil {
		t.Fatalf("open forward: %v", err)
	}
	msg := []byte("provider-tunnel")
	if _, err := fwd.Write(msg); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(fwd, buf); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(buf) != string(msg) {
		t.Fatalf("echo = %q, want %q", buf, msg)
	}
	_ = fwd.Close()
	_ = client.Close()

	e, ok := sink.find("tunnel")
	if !ok || e.Decision != "allow" || e.Target != "cont1" {
		t.Fatalf("tunnel audit = %+v, want allow cont1", e)
	}
}

// TestGateway_ProviderMasking proves the masking writer redacts the provider
// streamer's output before it reaches the client (the wrappers are
// provider-agnostic, so native targets are masked like SSH targets).
func TestGateway_ProviderMasking(t *testing.T) {
	defer goleak.VerifyNone(t, ignoreTunnelPoolSweep())

	mask, err := NewMaskRuleset([]string{"s3cr3t-token"}, nil)
	if err != nil {
		t.Fatalf("mask ruleset: %v", err)
	}
	ca := newEd25519Signer(t)
	fe := &fakeExec{banner: "value=s3cr3t-token\n"}
	rec := containerRecord()

	addr, stopGW := startGateway(t, Options{
		TrustedCAs:   []ssh.PublicKey{ca.PublicKey()},
		Records:      staticRecords(rec),
		ExecRegistry: fakeRegistry{ex: fe},
		MaskRules:    mask,
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

	if err := sess.RequestPty("xterm-256color", 24, 80, ssh.TerminalModes{ssh.ECHO: 1}); err != nil {
		t.Fatalf("pty: %v", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	var out bytes.Buffer
	sess.Stdout = &out
	if err := sess.Start("cont1"); err != nil {
		t.Fatalf("start: %v", err)
	}
	// No input: the banner is emitted, then stdin EOF ends the streamer and the
	// mask writer flushes the retained (masked) tail.
	_ = stdin.Close()
	if err := sess.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, maskReplacement) {
		t.Fatalf("stdout = %q, want the masked token %q", got, maskReplacement)
	}
	if strings.Contains(got, "s3cr3t-token") {
		t.Fatalf("stdout = %q leaked the secret", got)
	}
}
