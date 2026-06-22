//go:build integration

package integration

import (
	"bytes"
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
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	gossh "golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/sshclient"
	"github.com/shareed2k/honey/internal/webserver"
)

func init() {
	if os.Getenv("DOCKER_HOST") == "" {
		home, _ := os.UserHomeDir()
		sockets := []string{
			filepath.Join(home, ".colima", "default", "docker.sock"),
			filepath.Join(home, ".colima", "docker.sock"),
			filepath.Join(home, ".orbstack", "run", "docker.sock"),
		}
		for _, sock := range sockets {
			if _, err := os.Stat(sock); err == nil {
				os.Setenv("DOCKER_HOST", "unix://"+sock)
				os.Setenv("TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE", "/var/run/docker.sock")
				break
			}
		}
	}
}

var (
	cleanupMu    sync.Mutex
	cleanupFuncs []func()
)

func TestMain(m *testing.M) {
	code := m.Run()
	cleanupMu.Lock()
	for _, f := range cleanupFuncs {
		f()
	}
	cleanupMu.Unlock()
	os.Exit(code)
}

// ── PostgreSQL ───────────────────────────────────────────────────────────────

var (
	pgOnce     sync.Once
	pgConnStr  string
	pgStartErr error
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
			pgStartErr = err
			return
		}
		s, err := c.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			pgStartErr = err
			return
		}
		pgConnStr = s
		cleanupMu.Lock()
		cleanupFuncs = append(cleanupFuncs, func() { _ = c.Terminate(context.Background()) })
		cleanupMu.Unlock()
	})
	if pgStartErr != nil {
		t.Skipf("start postgres skipped: %v", pgStartErr)
	}
	return pgConnStr
}

// ── ClickHouse ───────────────────────────────────────────────────────────────

var (
	chOnce     sync.Once
	chDSN      string
	chStartErr error
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
			chStartErr = err
			return
		}
		host, err := c.Host(ctx)
		if err != nil {
			chStartErr = err
			return
		}
		port, err := c.MappedPort(ctx, "9000")
		if err != nil {
			chStartErr = err
			return
		}
		chDSN = fmt.Sprintf("clickhouse://default:test@%s:%s/testdb", host, port.Port())
		cleanupMu.Lock()
		cleanupFuncs = append(cleanupFuncs, func() { _ = c.Terminate(context.Background()) })
		cleanupMu.Unlock()
	})
	if chStartErr != nil {
		t.Skipf("start clickhouse skipped: %v", chStartErr)
	}
	return chDSN
}

// ── OpenSearch ───────────────────────────────────────────────────────────────

var (
	osOnce     sync.Once
	osAddr     string
	osStartErr error
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
				"OPENSEARCH_JAVA_OPTS":              "-Xms512m -Xmx512m",
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
			osStartErr = err
			return
		}
		host, err := c.Host(ctx)
		if err != nil {
			osStartErr = err
			return
		}
		port, err := c.MappedPort(ctx, "9200")
		if err != nil {
			osStartErr = err
			return
		}
		osAddr = fmt.Sprintf("https://%s:%s", host, port.Port())
		cleanupMu.Lock()
		cleanupFuncs = append(cleanupFuncs, func() { _ = c.Terminate(context.Background()) })
		cleanupMu.Unlock()
	})
	if osStartErr != nil {
		t.Skipf("start opensearch skipped: %v", osStartErr)
	}
	return osAddr
}

// ── SSH ──────────────────────────────────────────────────────────────────────

var (
	sshOnce     sync.Once
	sshHost     string
	sshPort     int
	sshKeyFile  string
	sshStartErr error
)

func startSSH(t *testing.T) (host string, port int, keyFile string) {
	t.Helper()
	sshOnce.Do(func() {
		// Generate ED25519 keypair for test auth.
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			sshStartErr = err
			return
		}
		sshPub, err := gossh.NewPublicKey(pub)
		if err != nil {
			sshStartErr = err
			return
		}
		authorizedKey := string(gossh.MarshalAuthorizedKey(sshPub))

		// Write private key to temp file (OpenSSH PEM format).
		privPEMBlock, err := gossh.MarshalPrivateKey(priv, "")
		if err != nil {
			sshStartErr = err
			return
		}
		keyPath, err := os.CreateTemp("", "integration_test_ed25519_*")
		if err != nil {
			sshStartErr = err
			return
		}
		keyPath.Close()
		if err := os.WriteFile(keyPath.Name(), pem.EncodeToMemory(privPEMBlock), 0o600); err != nil {
			sshStartErr = err
			return
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
			sshStartErr = err
			return
		}
		h, err := c.Host(ctx)
		if err != nil {
			sshStartErr = err
			return
		}
		p, err := c.MappedPort(ctx, "2222")
		if err != nil {
			sshStartErr = err
			return
		}
		sshHost = h
		sshPort = int(p.Num())
		cleanupMu.Lock()
		cleanupFuncs = append(cleanupFuncs, func() { _ = c.Terminate(context.Background()) })
		cleanupMu.Unlock()
	})
	if sshStartErr != nil {
		t.Skipf("start ssh skipped: %v", sshStartErr)
	}
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

func (c *testSSHClient) Upload(localPath, remotePath string) error {
	sftpClient, err := sftp.NewClient(c.c)
	if err != nil {
		return err
	}
	defer sftpClient.Close()

	in, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := sftpClient.MkdirAll(filepath.ToSlash(filepath.Dir(remotePath))); err != nil {
		return err
	}

	out, err := sftpClient.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
func (c *testSSHClient) Download(remotePath, localPath string) error {
	sftpClient, err := sftp.NewClient(c.c)
	if err != nil {
		return err
	}
	defer sftpClient.Close()

	src, err := sftpClient.Open(remotePath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}
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
func newTestDialer(containerHost string, containerPort int, keyFile string) DialerFunc {
	return DialerFunc(func(user, _ string, _ int, _ string) (hostexec.HostClient, error) {
		client, err := dialSSHTestContainer(user, containerHost, containerPort, keyFile)
		if err != nil {
			return nil, err
		}
		return &testSSHClient{c: client}, nil
	})
}

// newTestTunnelRunner returns a TunnelRunnerFunc that connects via the test SSH container.
func newTestTunnelRunner(containerHost string, containerPort int, keyFile string) TunnelRunnerFunc {
	return TunnelRunnerFunc(func(ctx context.Context, user, _ string, _ int, localFwd string, out io.Writer) error {
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

type DialerFunc func(user, host string, port int, keyFile string) (hostexec.HostClient, error)

func (f DialerFunc) Dial(user, host string, port int, keyFile string) (hostexec.HostClient, error) {
	return f(user, host, port, keyFile)
}

type TunnelRunnerFunc func(ctx context.Context, user, host string, port int, localFwd string, out io.Writer) error

type testRegistry struct {
	Dialer DialerFunc
	Tunnel TunnelRunnerFunc
}

func (r *testRegistry) ForRecord(rec hosts.Record) hostexec.Executor {
	return &testExecutor{reg: r, rec: rec}
}

func (r *testRegistry) Reconfigure(cfg *config.File) {}

func (r *testRegistry) RunSSHTunnel(ctx context.Context, user, host string, sshPort int, localFwd string, out io.Writer) error {
	if r.Tunnel != nil {
		return r.Tunnel(ctx, user, host, sshPort, localFwd, out)
	}
	return fmt.Errorf("Tunnel not implemented")
}

func (r *testRegistry) BorrowSSH(user string, hop hosts.Record) (any, bool) {
	return nil, false
}

type testExecutor struct {
	reg *testRegistry
	rec hosts.Record
}

func (e *testExecutor) Dial(user string, r hosts.Record) (hostexec.HostClient, error) {
	return e.reg.Dialer(user, "", 0, "")
}

func (e *testExecutor) RunInteractive(user string, r hosts.Record) error {
	return fmt.Errorf("not implemented")
}

func (e *testExecutor) RunTunnel(ctx context.Context, user string, r hosts.Record, localFwd string, out io.Writer) error {
	if e.reg.Tunnel != nil {
		return e.reg.Tunnel(ctx, user, "", 0, localFwd, out)
	}
	return fmt.Errorf("not implemented")
}

func (e *testExecutor) DialUpstream(ctx context.Context, user string, r hosts.Record, address string) (net.Conn, error) {
	return nil, fmt.Errorf("not implemented")
}

func (c *testSSHClient) SupportsKVTunnel() bool {
	return false
}

// ── Docker-in-Docker ─────────────────────────────────────────────────────────

var (
	dindOnce     sync.Once
	dindHost     string
	dindStartErr error
)

func startDinD(t *testing.T) string {
	t.Helper()
	dindOnce.Do(func() {
		ctx := context.Background()
		req := testcontainers.ContainerRequest{
			Image:        "docker:dind",
			ExposedPorts: []string{"2375/tcp"},
			Privileged:   true,
			Env: map[string]string{
				"DOCKER_TLS_CERTDIR": "", // Disable TLS for testing
			},
			WaitingFor: wait.ForListeningPort("2375/tcp").WithStartupTimeout(60 * time.Second),
		}
		c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		})
		if err != nil {
			dindStartErr = err
			return
		}
		host, err := c.Host(ctx)
		if err != nil {
			dindStartErr = err
			return
		}
		port, err := c.MappedPort(ctx, "2375")
		if err != nil {
			dindStartErr = err
			return
		}
		dindHost = fmt.Sprintf("tcp://%s:%s", host, port.Port())

		cleanupMu.Lock()
		cleanupFuncs = append(cleanupFuncs, func() { _ = c.Terminate(context.Background()) })
		cleanupMu.Unlock()
	})
	if dindStartErr != nil {
		t.Skipf("start dind skipped: %v", dindStartErr)
	}
	return dindHost
}

// ── K3s ──────────────────────────────────────────────────────────────────────

var (
	k3sOnce       sync.Once
	k3sKubeConfig []byte
	k3sStartErr   error
)

func startK3s(t *testing.T) []byte {
	t.Helper()
	k3sOnce.Do(func() {
		ctx := context.Background()
		req := testcontainers.ContainerRequest{
			Image:        "rancher/k3s:latest",
			ExposedPorts: []string{"6443/tcp"},
			Privileged:   true,
			Cmd: []string{
				"server",
				"--disable=traefik",
				"--disable=servicelb",
				"--disable=metrics-server",
			},
			WaitingFor: wait.ForLog("Node controller sync successful").WithStartupTimeout(120 * time.Second),
		}
		c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		})
		if err != nil {
			k3sStartErr = err
			return
		}

		// Read the kubeconfig from the container
		r, err := c.CopyFileFromContainer(ctx, "/etc/rancher/k3s/k3s.yaml")
		if err != nil {
			k3sStartErr = fmt.Errorf("copy kubeconfig: %w", err)
			return
		}
		defer r.Close()

		kubeConfigBytes, err := io.ReadAll(r)
		if err != nil {
			k3sStartErr = fmt.Errorf("read kubeconfig: %w", err)
			return
		}

		// Rewrite the host/port to point to localhost mapped port
		host, err := c.Host(ctx)
		if err != nil {
			k3sStartErr = fmt.Errorf("get host: %w", err)
			return
		}

		port, err := c.MappedPort(ctx, "6443")
		if err != nil {
			k3sStartErr = fmt.Errorf("get mapped port: %w", err)
			return
		}

		kubeConfigBytes = bytes.ReplaceAll(kubeConfigBytes, []byte("https://127.0.0.1:6443"), []byte(fmt.Sprintf("https://%s:%s", host, port.Port())))

		k3sKubeConfig = kubeConfigBytes

		cleanupMu.Lock()
		cleanupFuncs = append(cleanupFuncs, func() { _ = c.Terminate(context.Background()) })
		cleanupMu.Unlock()
	})
	if k3sStartErr != nil {
		t.Skipf("start k3s skipped: %v", k3sStartErr)
	}
	return k3sKubeConfig
}

// ── Registry ─────────────────────────────────────────────────────────────────

var (
	registryOnce     sync.Once
	registryAddr     string
	registryStartErr error
)

// startRegistry boots a local Docker registry (registry:2) and returns its
// address as 127.0.0.1:<port>. Using a loopback address makes the Docker daemon
// treat it as an insecure registry, so no daemon reconfiguration is needed.
func startRegistry(t *testing.T) string {
	t.Helper()
	registryOnce.Do(func() {
		ctx := context.Background()
		req := testcontainers.ContainerRequest{
			Image:        "registry:2",
			ExposedPorts: []string{"5000/tcp"},
			WaitingFor:   wait.ForListeningPort("5000/tcp").WithStartupTimeout(60 * time.Second),
		}
		c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		})
		if err != nil {
			registryStartErr = err
			return
		}
		port, err := c.MappedPort(ctx, "5000")
		if err != nil {
			registryStartErr = err
			return
		}
		registryAddr = fmt.Sprintf("127.0.0.1:%s", port.Port())

		cleanupMu.Lock()
		cleanupFuncs = append(cleanupFuncs, func() { _ = c.Terminate(context.Background()) })
		cleanupMu.Unlock()
	})
	if registryStartErr != nil {
		t.Skipf("start registry skipped: %v", registryStartErr)
	}
	return registryAddr
}

// StartLocalForward starts a local port forward.
func (e *testSSHClient) StartLocalForward(_ context.Context, _ string, _ int, _ string, _ int) (host string, port int, stop func(), err error) {
	return "", 0, nil, fmt.Errorf("tunneling not supported on this transport")
}

// StartRemoteForward starts a remote port forward.
func (e *testSSHClient) StartRemoteForward(_ context.Context, _ string, _ int, _ string, _ int) (remAddr string, stop func(), err error) {
	return "", nil, fmt.Errorf("tunneling not supported on this transport")
}

// StartDynamicForward starts a dynamic port forward.
func (e *testSSHClient) StartDynamicForward(_ context.Context, _ string, _ int) (host string, port int, stop func(), err error) {
	return "", 0, nil, fmt.Errorf("tunneling not supported on this transport")
}

// StartUDPRelay starts a UDP relay.
func (e *testSSHClient) StartUDPRelay(_ context.Context, _ string, _ int, _ string, _ int, _ bool) (host string, port int, stop func(), err error) {
	return "", 0, nil, fmt.Errorf("tunneling not supported on this transport")
}

// StartTunForward starts a TUN forward.
func (e *testSSHClient) StartTunForward(_ context.Context, _ string, _ string, _ int, _, _ int) (tunName string, stop func(), err error) {
	return "", nil, fmt.Errorf("tunneling not supported on this transport")
}
