//go:build integration

package integration

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/sshclient"
)

// TestUnixSocketTunnel_E2E is the real end-to-end proof of the unix-socket
// (OpenSSH direct-streamlocal) tunnel: it stands up a genuine sshd container,
// runs a unix-socket echo server inside it, then uses the production
// sshclient.StartLocalSocketForward against that REAL sshd and round-trips
// bytes through the operator-side unix socket. Unlike the unit test (which
// drives a hand-rolled fake sshd) this verifies a real OpenSSH server accepts
// our direct-streamlocal@openssh.com channel-open payload and that
// AllowStreamLocalForwarding is honoured.
//
// This is the postgres `peer` transport path: over SSH, the process connecting
// to the remote socket is sshd as the login user, which is what makes peer work.
//
// Skips (not fails) when socat cannot be provided inside the container — host
// networking / package egress is environment-dependent (see the plan's
// skip-if-unrunnable note for the docker-gated e2e).
func TestUnixSocketTunnel_E2E(t *testing.T) {
	host, port, keyFile := startSSH(t)
	client, err := dialSSHTestContainer("testuser", host, port, keyFile)
	if err != nil {
		t.Fatalf("dial ssh container: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if !ensureSocat(t, client) {
		t.Skip("socat unavailable in ssh container and cannot be installed (no egress); skipping unix-tunnel e2e")
	}

	const remoteSock = "/tmp/honey-e2e-echo.sock"
	_, _ = runSSH(client, "rm -f "+remoteSock) // clear any stale socket from a prior run

	// A unix-socket echo server inside the container, held open for the test by
	// a dedicated session; closing the session (cleanup) tears socat down.
	echoSess, err := client.NewSession()
	if err != nil {
		t.Fatalf("new echo session: %v", err)
	}
	t.Cleanup(func() { _ = echoSess.Close() })
	go func() {
		_ = echoSess.Run(fmt.Sprintf("socat UNIX-LISTEN:%s,fork EXEC:/bin/cat", remoteSock))
	}()
	waitForRemoteSocket(t, client, remoteSock, 20*time.Second)

	// Operator-side local socket. A short /tmp base keeps the path under the
	// sun_path length limit (macOS TMPDIR is far too long).
	dir, err := os.MkdirTemp("/tmp", "hpg")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	local := filepath.Join(dir, ".s.PGSQL.5432")

	// The production code path under test: forward the container's unix socket
	// to a local one over a real direct-streamlocal channel.
	path, stop, err := sshclient.StartLocalSocketForward(context.Background(), client, local, remoteSock)
	if err != nil {
		t.Fatalf("StartLocalSocketForward: %v", err)
	}
	t.Cleanup(stop)

	c, err := net.DialTimeout("unix", path, 5*time.Second)
	if err != nil {
		t.Fatalf("dial local socket: %v", err)
	}
	defer func() { _ = c.Close() }()

	want := []byte("ping-over-real-sshd\n")
	if _, err := c.Write(want); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatalf("read echo through real sshd streamlocal: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("round trip mismatch: got %q, want %q", got, want)
	}

	// Round-trip proves the tunnel is up; stop() must then remove the local
	// socket file (cleanup also calls it, which is idempotent).
	stop()
	if _, statErr := os.Stat(local); !os.IsNotExist(statErr) {
		t.Fatalf("local socket not removed after stop: err=%v", statErr)
	}
}

// runSSH runs one command on the container and returns combined stdout.
func runSSH(client *gossh.Client, cmd string) (string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer func() { _ = sess.Close() }()
	out, err := sess.CombinedOutput(cmd)
	return string(out), err
}

// ensureSocat makes socat available in the (Alpine linuxserver) container,
// installing it via apk if needed. Returns false if it is absent and cannot be
// installed (e.g. no network egress), so the caller can skip.
func ensureSocat(t *testing.T, client *gossh.Client) bool {
	t.Helper()
	if out, _ := runSSH(client, "command -v socat || true"); strings.TrimSpace(out) != "" {
		return true
	}
	// linuxserver/openssh-server is Alpine with SUDO_ACCESS; try apk.
	_, _ = runSSH(client, "sudo apk add --no-cache socat >/dev/null 2>&1 || apk add --no-cache socat >/dev/null 2>&1 || true")
	out, _ := runSSH(client, "command -v socat || true")
	return strings.TrimSpace(out) != ""
}

// waitForRemoteSocket polls until the given unix socket exists in the container.
func waitForRemoteSocket(t *testing.T, client *gossh.Client, socketPath string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, _ := runSSH(client, "test -S "+socketPath+" && echo ok || true")
		if strings.TrimSpace(out) == "ok" {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("remote socket %s never appeared within %s", socketPath, timeout)
}
