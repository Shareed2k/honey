//go:build integration

package cli

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/hosts"
)

// directTcpipExtraData mirrors golang.org/x/crypto/ssh's internal
// channelOpenDirectMsg (RFC 4254 7.2): the payload sent by (*ssh.Client).Dial
// on a "direct-tcpip" channel open. Field order (not names) matters for
// ssh.Unmarshal.
type directTcpipExtraData struct {
	DestAddr   string
	DestPort   uint32
	OriginAddr string
	OriginPort uint32
}

// directStreamLocalExtraData mirrors OpenSSH's direct-streamlocal@openssh.com
// channel-open payload: a unix socket path plus two reserved fields. Field
// order (not names) matters for ssh.Unmarshal.
type directStreamLocalExtraData struct {
	SocketPath string
	Reserved0  string
	Reserved1  uint32
}

// startLoopbackSSHD starts an in-process SSH server on 127.0.0.1 that accepts
// only authorizedKey and, for "direct-tcpip" channels, actually dials the
// requested destination and relays bytes both ways — a real (if minimal)
// port-forwarding sshd, so DialUpstream's whole path (ssh dial, auth,
// leaf.Dial "direct-tcpip") is exercised end to end.
func startLoopbackSSHD(t *testing.T, authorizedKey ssh.PublicKey) (port int, stop func()) {
	t.Helper()

	hostKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatalf("signer from host key: %v", err)
	}

	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if string(key.Marshal()) == string(authorizedKey.Marshal()) {
				return &ssh.Permissions{}, nil
			}
			return nil, errUnauthorizedKey
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			raw, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go serveLoopbackSSHDConn(raw, cfg)
		}
	}()

	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split listener addr: %v", err)
	}
	p, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse listener port: %v", err)
	}

	return p, func() {
		_ = ln.Close()
		<-done
	}
}

var errUnauthorizedKey = &sshdAuthError{"unauthorized key"}

type sshdAuthError struct{ msg string }

func (e *sshdAuthError) Error() string { return e.msg }

func serveLoopbackSSHDConn(raw net.Conn, cfg *ssh.ServerConfig) {
	sc, chans, reqs, err := ssh.NewServerConn(raw, cfg)
	if err != nil {
		_ = raw.Close()
		return
	}
	defer func() { _ = sc.Close() }()
	go ssh.DiscardRequests(reqs)

	for newCh := range chans {
		switch newCh.ChannelType() {
		case "direct-tcpip":
			var extra directTcpipExtraData
			if err := ssh.Unmarshal(newCh.ExtraData(), &extra); err != nil {
				_ = newCh.Reject(ssh.ConnectionFailed, "bad direct-tcpip payload")
				continue
			}
			dst, err := net.DialTimeout("tcp", net.JoinHostPort(extra.DestAddr, strconv.Itoa(int(extra.DestPort))), 5*time.Second)
			if err != nil {
				_ = newCh.Reject(ssh.ConnectionFailed, "dial destination failed")
				continue
			}
			ch, inReqs, err := newCh.Accept()
			if err != nil {
				_ = dst.Close()
				continue
			}
			go ssh.DiscardRequests(inReqs)
			go relay(ch, dst)
		case "direct-streamlocal@openssh.com":
			var extra directStreamLocalExtraData
			if err := ssh.Unmarshal(newCh.ExtraData(), &extra); err != nil {
				_ = newCh.Reject(ssh.ConnectionFailed, "bad direct-streamlocal payload")
				continue
			}
			dst, err := net.DialTimeout("unix", extra.SocketPath, 5*time.Second)
			if err != nil {
				_ = newCh.Reject(ssh.ConnectionFailed, "dial socket failed")
				continue
			}
			ch, inReqs, err := newCh.Accept()
			if err != nil {
				_ = dst.Close()
				continue
			}
			go ssh.DiscardRequests(inReqs)
			go relay(ch, dst)
		default:
			_ = newCh.Reject(ssh.UnknownChannelType, "unsupported")
		}
	}
}

// relay pipes bytes both ways between an SSH channel and a TCP conn, closing
// both once either side is done.
func relay(ch ssh.Channel, conn net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(conn, ch)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(ch, conn)
		done <- struct{}{}
	}()
	<-done
	_ = ch.Close()
	_ = conn.Close()
	<-done
}

// startLoopbackEcho starts a plain TCP echo listener used as the "upstream"
// target that DialUpstream's returned net.Conn should reach through the SSH
// tunnel.
func startLoopbackEcho(t *testing.T) (addr string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen echo: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()
	return ln.Addr().String(), func() {
		_ = ln.Close()
		<-done
	}
}

// startLoopbackUnixEcho starts a unix-socket echo listener used as the
// "upstream" target that DialUpstream's returned net.Conn should reach through
// the SSH direct-streamlocal channel. A short /tmp base keeps the socket path
// under the sun_path length limit.
func startLoopbackUnixEcho(t *testing.T) (socketPath string, stop func()) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "hpg")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	socketPath = filepath.Join(dir, "echo.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix echo: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()
	return socketPath, func() {
		_ = ln.Close()
		<-done
		_ = os.RemoveAll(dir)
	}
}

// TestSSHFallbackExecutor_DialUpstream_RoundTrip is the real end-to-end
// verification of Task 1: it dials an in-process sshd via
// sshFallbackExecutor.DialUpstream, then round-trips bytes through the
// returned net.Conn to a TCP echo server reachable only via the SSH
// "direct-tcpip" channel — proving the whole ssh dial + leaf.Dial path works,
// not just the closer-wrapper in isolation.
//
// Gated behind the integration build tag: it needs a sandboxed $HOME (so the
// real DialHoneyHost path, including known_hosts accept-new, never touches the
// developer's actual ~/.ssh) and disables shelling out to the system `ssh -G`
// so config resolution is deterministic across machines.
func TestSSHFallbackExecutor_DialUpstream_RoundTrip(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("HONEY_SSH_OPENSSH_G", "0")

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatalf("signer from client key: %v", err)
	}

	keyPath := filepath.Join(tmpHome, "id_rsa_test")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(keyPath, pemBytes, 0o600); err != nil {
		t.Fatalf("write identity file: %v", err)
	}

	sshdPort, stopSSHD := startLoopbackSSHD(t, signer.PublicKey())
	defer stopSSHD()

	echoAddr, stopEcho := startLoopbackEcho(t)
	defer stopEcho()

	rec := hosts.Record{PrimaryIP: "127.0.0.1"}
	rec = hosts.CloneWithMetaSSHPort(rec, sshdPort)
	rec = hosts.CloneWithMetaSSHIdentityFile(rec, keyPath)

	e := &sshFallbackExecutor{}
	conn, err := e.DialUpstream(context.Background(), "honeytest", rec, echoAddr)
	if err != nil {
		t.Fatalf("DialUpstream: %v", err)
	}
	defer func() { _ = conn.Close() }()

	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	want := []byte("ping-through-ssh-tunnel")
	if _, err := conn.Write(want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := make([]byte, len(want))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("round trip mismatch: got %q, want %q", got, want)
	}
}

// TestSSHFallbackExecutor_DialUpstream_UnixRoundTrip proves the mesh unix path:
// a "unix:<path>" target makes DialUpstream open a direct-streamlocal channel
// on the leaf, reaching a unix-socket echo server that no TCP dial could hit.
func TestSSHFallbackExecutor_DialUpstream_UnixRoundTrip(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("HONEY_SSH_OPENSSH_G", "0")

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatalf("signer from client key: %v", err)
	}
	keyPath := filepath.Join(tmpHome, "id_rsa_test")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(keyPath, pemBytes, 0o600); err != nil {
		t.Fatalf("write identity file: %v", err)
	}

	sshdPort, stopSSHD := startLoopbackSSHD(t, signer.PublicKey())
	defer stopSSHD()

	socketPath, stopEcho := startLoopbackUnixEcho(t)
	defer stopEcho()

	rec := hosts.Record{PrimaryIP: "127.0.0.1"}
	rec = hosts.CloneWithMetaSSHPort(rec, sshdPort)
	rec = hosts.CloneWithMetaSSHIdentityFile(rec, keyPath)

	e := &sshFallbackExecutor{}
	conn, err := e.DialUpstream(context.Background(), "honeytest", rec, "unix:"+socketPath)
	if err != nil {
		t.Fatalf("DialUpstream unix: %v", err)
	}
	defer func() { _ = conn.Close() }()

	want := []byte("ping-through-streamlocal")
	if _, err := conn.Write(want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := make([]byte, len(want))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("round trip mismatch: got %q, want %q", got, want)
	}
}
