package sshgateway

import (
	"context"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"
	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/policy"
)

// startEchoServer stands up a plain TCP echo listener on loopback and returns
// its port and a stop func that drains every accepted connection goroutine (so
// goleak stays clean).
func startEchoServer(t *testing.T) (port int, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo listen: %v", err)
	}
	var wg sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			wg.Add(1)
			go func(conn net.Conn) {
				defer wg.Done()
				defer func() { _ = conn.Close() }()
				_, _ = io.Copy(conn, conn)
			}(c)
		}
	}()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(portStr)
	return p, func() {
		_ = ln.Close()
		<-done
		wg.Wait()
	}
}

// TestGateway_DirectTCPIP_Forward drives the `ssh -L` path end to end: the client
// opens a direct-tcpip channel to <resource>:<echoPort>; the gateway resolves the
// resource, SSH-dials the target sshd, opens 127.0.0.1:<echoPort> on it (the
// in-process echo listener), and bridges — so bytes round-trip.
func TestGateway_DirectTCPIP_Forward(t *testing.T) {
	// engine's GlobalTunnelPool sweepLoop is a process-lifetime singleton, not a
	// per-session leak (same ignore as the Phase A leak test).
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("github.com/shareed2k/honey/internal/engine.(*GlobalTunnelPool).sweepLoop"),
	)

	sandboxSSHEnv(t)

	echoPort, stopEcho := startEchoServer(t)
	defer stopEcho()

	identity, pub := targetKey(t)
	port, stopTarget := startTargetSSHD(t, pub)
	defer stopTarget()

	ca := newEd25519Signer(t)
	sink := &memSink{}
	rec := targetRecord("postgres-demo", identity, port)

	addr, stopGW := startGateway(t, Options{
		TrustedCAs: []ssh.PublicKey{ca.PublicKey()},
		AuditSink:  sink,
		Records:    staticRecords(rec),
	})
	defer stopGW()

	client, err := dialGateway(t, addr, "alice", signedCertAuth(t, ca, "alice", "alice@corp", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatalf("dial gateway: %v", err)
	}

	fwd, err := client.Dial("tcp", net.JoinHostPort("postgres-demo", strconv.Itoa(echoPort)))
	if err != nil {
		t.Fatalf("open forward: %v", err)
	}

	msg := []byte("phase-b-echo")
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
	if !ok {
		t.Fatalf("no tunnel audit event")
	}
	if e.Decision != "allow" || e.Target != "postgres-demo" {
		t.Fatalf("tunnel audit = %+v, want allow postgres-demo", e)
	}
	if e.Extra["dest_port"] != strconv.Itoa(echoPort) {
		t.Fatalf("tunnel audit dest_port = %q, want %d", e.Extra["dest_port"], echoPort)
	}
	if e.Source != "ssh-gateway" {
		t.Fatalf("source = %q, want ssh-gateway", e.Source)
	}
}

// TestGateway_DirectTCPIP_Rejected covers the two clean-refusal paths: an OPA
// tunnel deny and an unknown resource. Both must fail the channel-open without
// dialing a target.
func TestGateway_DirectTCPIP_Rejected(t *testing.T) {
	sandboxSSHEnv(t)

	const denySrc = `package honey
import rego.v1
default allow := true
default deny_reason := ""
allow := false if input.action == "tunnel"
deny_reason := "tunnel blocked by test policy" if input.action == "tunnel"
`
	denyEnf, err := policy.NewFromSource(context.Background(), "deny.rego", denySrc)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}

	tests := []struct {
		name     string
		enforcer *policy.Enforcer
		records  func(context.Context) ([]hosts.Record, error)
		dest     string
		wantDeny bool
	}{
		{
			name:     "opa tunnel deny",
			enforcer: denyEnf,
			records:  staticRecords(hosts.Record{Provider: "local", Name: "web1", PrimaryIP: "127.0.0.1"}),
			dest:     "web1:5432",
			wantDeny: true,
		},
		{
			name:     "unknown resource",
			enforcer: nil,
			records:  staticRecords(),
			dest:     "nope:5432",
			wantDeny: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ca := newEd25519Signer(t)
			sink := &memSink{}
			addr, stopGW := startGateway(t, Options{
				TrustedCAs: []ssh.PublicKey{ca.PublicKey()},
				Enforcer:   tc.enforcer,
				AuditSink:  sink,
				Records:    tc.records,
			})
			defer stopGW()

			client, err := dialGateway(t, addr, "alice", signedCertAuth(t, ca, "alice", "alice@corp", time.Now().Add(time.Hour)))
			if err != nil {
				t.Fatalf("dial gateway: %v", err)
			}
			defer func() { _ = client.Close() }()

			fwd, err := client.Dial("tcp", tc.dest)
			if err == nil {
				_ = fwd.Close()
				t.Fatalf("expected forward to be rejected, got success")
			}

			e, ok := sink.find("tunnel")
			if !ok {
				t.Fatalf("no tunnel audit event")
			}
			if e.Decision != "deny" {
				t.Fatalf("tunnel audit = %+v, want deny", e)
			}
			if tc.wantDeny && e.DenyReason != "tunnel blocked by test policy" {
				t.Fatalf("deny reason = %q, want policy reason", e.DenyReason)
			}
		})
	}
}
