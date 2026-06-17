//go:build integration

package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	gossh "golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/sshclient"
	"github.com/shareed2k/honey/internal/webserver"
)

// ── PostgreSQL ───────────────────────────────────────────────────────────────

var (
	pgOnce    sync.Once
	pgConnStr string
)

func startPostgres(t *testing.T) string {
	t.Helper()
	pgOnce.Do(func() {
		ctx := context.Background()
		c, err := tcpostgres.RunContainer(
			ctx,
			testcontainers.WithImage("postgres:16-alpine"),
			tcpostgres.WithDatabase("testdb"),
			tcpostgres.WithUsername("test"),
			tcpostgres.WithPassword("test"),
			testcontainers.WithWaitStrategy(
				wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second),
			),
		)
		if err != nil {
			t.Fatalf("start postgres: %v", err)
		}
		s, err := c.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			t.Fatalf("postgres connection string: %v", err)
		}
		pgConnStr = s
	})
	return pgConnStr
}

// ── ClickHouse ───────────────────────────────────────────────────────────────

var (
	chOnce sync.Once
	chDSN  string
)

func startClickHouse(t *testing.T) string {
	t.Helper()
	chOnce.Do(func() {
		ctx := context.Background()
		req := testcontainers.ContainerRequest{
			Image:        "clickhouse/clickhouse-server:latest",
			ExposedPorts: []string{"9000/tcp", "8123/tcp"},
			// Wait for HTTP /ping (8123) — port 9000 opens before native protocol is ready.
			WaitingFor: wait.ForHTTP("/ping").WithPort("8123/tcp").
				WithStartupTimeout(120 * time.Second),
			Env: map[string]string{
				"CLICKHOUSE_DB":       "testdb",
				"CLICKHOUSE_USER":     "default",
				"CLICKHOUSE_PASSWORD": "test",
			},
		}
		c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		})
		if err != nil {
			t.Fatalf("start clickhouse: %v", err)
		}
		host, err := c.Host(ctx)
		if err != nil {
			t.Fatalf("clickhouse host: %v", err)
		}
		port, err := c.MappedPort(ctx, "9000")
		if err != nil {
			t.Fatalf("clickhouse port: %v", err)
		}
		chDSN = fmt.Sprintf("clickhouse://default:test@%s:%s/testdb", host, port.Port())
	})
	return chDSN
}

// ── OpenSearch ───────────────────────────────────────────────────────────────

var (
	osOnce sync.Once
	osAddr string
)

func startOpenSearch(t *testing.T) string {
	t.Helper()
	osOnce.Do(func() {
		ctx := context.Background()
		// Use GenericContainer directly — the tcopensearch module caps its wait at 60s
		// regardless of WithWaitStrategy. OpenSearch with all plugins needs ~2-3 min.
		// OpenSearch 2.12+ requires OPENSEARCH_INITIAL_ADMIN_PASSWORD. Demo config
		// enables HTTPS on port 9200 (self-signed cert) — use TLS wait strategy.
		insecureTLS := &tls.Config{InsecureSkipVerify: true} //nolint:gosec // integration test only
		req := testcontainers.ContainerRequest{
			Image:        "opensearchproject/opensearch:2",
			ExposedPorts: []string{"9200/tcp"},
			Env: map[string]string{
				"discovery.type":                    "single-node",
				"OPENSEARCH_INITIAL_ADMIN_PASSWORD": "Qx7#nBm2pLv!",
			},
			WaitingFor: wait.ForHTTP("/").WithPort("9200/tcp").
				WithTLS(true, insecureTLS).
				WithStartupTimeout(300 * time.Second).
				WithStatusCodeMatcher(func(status int) bool {
					return status == http.StatusOK || status == http.StatusUnauthorized
				}),
		}
		c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		})
		if err != nil {
			t.Fatalf("start opensearch: %v", err)
		}
		host, err := c.Host(ctx)
		if err != nil {
			t.Fatalf("opensearch host: %v", err)
		}
		port, err := c.MappedPort(ctx, "9200")
		if err != nil {
			t.Fatalf("opensearch port: %v", err)
		}
		osAddr = fmt.Sprintf("https://%s:%s", host, port.Port())
	})
	return osAddr
}

// ── SSH ──────────────────────────────────────────────────────────────────────

var (
	sshOnce    sync.Once
	sshHost    string
	sshPort    int
	sshKeyFile string
)

func startSSH(t *testing.T) (host string, port int, keyFile string) {
	t.Helper()
	sshOnce.Do(func() {
		// Generate ED25519 keypair for test auth.
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("generate ssh key: %v", err)
		}
		sshPub, err := gossh.NewPublicKey(pub)
		if err != nil {
			t.Fatalf("ssh public key: %v", err)
		}
		authorizedKey := string(gossh.MarshalAuthorizedKey(sshPub))

		// Write private key to temp file (OpenSSH PEM format).
		privPEMBlock, err := gossh.MarshalPrivateKey(priv, "")
		if err != nil {
			t.Fatalf("marshal ssh private key: %v", err)
		}
		keyPath, err := os.CreateTemp("", "integration_test_ed25519_*")
		if err != nil {
			t.Fatalf("create key temp file: %v", err)
		}
		keyPath.Close()
		if err := os.WriteFile(keyPath.Name(), pem.EncodeToMemory(privPEMBlock), 0o600); err != nil {
			t.Fatalf("write ssh key: %v", err)
		}
		sshKeyFile = keyPath.Name()

		ctx := context.Background()
		req := testcontainers.ContainerRequest{
			Image:        "lscr.io/linuxserver/openssh-server:latest",
			ExposedPorts: []string{"2222/tcp"},
			Env: map[string]string{
				"PUID":       "1000",
				"PGID":       "1000",
				"PUBLIC_KEY": authorizedKey,
				"USER_NAME":  "testuser",
			},
			WaitingFor: wait.ForListeningPort("2222/tcp").WithStartupTimeout(120 * time.Second),
		}
		c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		})
		if err != nil {
			t.Fatalf("start ssh: %v", err)
		}
		h, err := c.Host(ctx)
		if err != nil {
			t.Fatalf("ssh host: %v", err)
		}
		p, err := c.MappedPort(ctx, "2222")
		if err != nil {
			t.Fatalf("ssh port: %v", err)
		}
		sshHost = h
		sshPort = int(p.Num())
	})
	return sshHost, sshPort, sshKeyFile
}

// ── SSH test client ──────────────────────────────────────────────────────────

// testSSHClient wraps *gossh.Client to satisfy hostexec.HostClient.
// Only Run/RunWithStreams/Close are implemented; file ops return errors.
type testSSHClient struct{ c *gossh.Client }

func (c *testSSHClient) Run(cmd string) ([]byte, error) {
	sess, err := c.c.NewSession()
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	return sess.Output(cmd)
}

func (c *testSSHClient) RunWithStreams(cmd string, stdin io.Reader, stdout, stderr io.Writer) error {
	sess, err := c.c.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	sess.Stdin = stdin
	sess.Stdout = stdout
	sess.Stderr = stderr
	return sess.Run(cmd)
}

func (c *testSSHClient) Upload(_, _ string) error   { return fmt.Errorf("not implemented") }
func (c *testSSHClient) Download(_, _ string) error { return fmt.Errorf("not implemented") }
func (c *testSSHClient) ListRemoteDir(_ string) ([]hostexec.RemoteFileEntry, error) {
	return nil, fmt.Errorf("not implemented")
}

func (c *testSSHClient) StatRemote(_ string) (hostexec.RemoteFileEntry, error) {
	return hostexec.RemoteFileEntry{}, fmt.Errorf("not implemented")
}

func (c *testSSHClient) MkdirAllRemote(_ string) error { return fmt.Errorf("not implemented") }

func (c *testSSHClient) RemoveRemote(_ string, _ bool) error { return fmt.Errorf("not implemented") }
func (c *testSSHClient) Close() error                        { return c.c.Close() }

// dialSSHTestContainer dials the test SSH container with InsecureIgnoreHostKey.
func dialSSHTestContainer(user, containerHost string, containerPort int, keyFile string) (*gossh.Client, error) {
	keyBytes, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("read key: %w", err)
	}
	signer, err := gossh.ParsePrivateKey(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("parse key: %w", err)
	}
	cfg := &gossh.ClientConfig{
		User:            user,
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(), //nolint:gosec // integration test only
		Timeout:         15 * time.Second,
	}
	addr := fmt.Sprintf("%s:%d", containerHost, containerPort)
	return gossh.Dial("tcp", addr, cfg)
}

// newTestDialer returns a DialerFunc that connects to the test SSH container.
func newTestDialer(containerHost string, containerPort int, keyFile string) hostexec.DialerFunc {
	return hostexec.DialerFunc(func(user, _ string, _ int, _ string) (hostexec.HostClient, error) {
		client, err := dialSSHTestContainer(user, containerHost, containerPort, keyFile)
		if err != nil {
			return nil, err
		}
		return &testSSHClient{c: client}, nil
	})
}

// newTestTunnelRunner returns a TunnelRunnerFunc that connects via the test SSH container.
func newTestTunnelRunner(containerHost string, containerPort int, keyFile string) hostexec.TunnelRunnerFunc {
	return hostexec.TunnelRunnerFunc(func(ctx context.Context, user, _ string, _ int, localFwd string, out io.Writer) error {
		localPortStr, remoteHost, remotePortStr, err := sshclient.ParseLocalForward(localFwd)
		if err != nil {
			return fmt.Errorf("parse local forward: %w", err)
		}
		localPort, _ := strconv.Atoi(localPortStr)
		remotePort, _ := strconv.Atoi(remotePortStr)

		client, err := dialSSHTestContainer(user, containerHost, containerPort, keyFile)
		if err != nil {
			return fmt.Errorf("ssh dial: %w", err)
		}
		defer client.Close()

		_, _, stop, err := sshclient.StartLocalForward(ctx, client, "127.0.0.1", localPort, remoteHost, remotePort)
		if err != nil {
			return fmt.Errorf("start local forward: %w", err)
		}
		defer stop()

		if out != nil {
			fmt.Fprintf(out, "Forwarding 127.0.0.1:%d -> %s:%d via test SSH\n", localPort, remoteHost, remotePort)
		}
		<-ctx.Done()
		return nil
	})
}

// ── Web server helper ────────────────────────────────────────────────────────

// freePort finds an available TCP port on 127.0.0.1.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// newTestServer boots a webserver.Server on a known free port and returns its base URL.
// The server is shut down automatically via t.Cleanup.
func newTestServer(t *testing.T, opts webserver.Options) string {
	t.Helper()
	port := freePort(t)
	opts.ListenAddr = fmt.Sprintf("127.0.0.1:%d", port)
	if opts.Token == "" {
		opts.Token = "test-token"
	}

	ready := make(chan struct{}, 1)
	opts.OnReady = func() { close(ready) }

	srv, err := webserver.NewServer(opts)
	if err != nil {
		t.Fatalf("webserver.NewServer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	startErr := make(chan error, 1)
	go func() {
		if err := srv.Start(ctx); err != nil {
			startErr <- err
		}
	}()

	select {
	case <-ready:
	case err := <-startErr:
		t.Fatalf("server failed to start: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("server startup timeout")
	}

	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

// authHeader returns the Authorization header for the test token.
func authHeader() string { return "Bearer test-token" }
