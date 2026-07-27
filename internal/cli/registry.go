package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/all"
	"github.com/shareed2k/honey/internal/searchrun"
	"github.com/shareed2k/honey/internal/sshclient"
	"golang.org/x/crypto/ssh"
)

var globalSearchRegistry *searchrun.Registry

// GetSearchRegistry returns the global search registry, initializing it if necessary.
func GetSearchRegistry() *searchrun.Registry {
	if globalSearchRegistry == nil {
		globalSearchRegistry = searchrun.NewRegistry(all.Factories(all.Deps{
			K8sInteractive:    engine.K8sInteractiveRunner(),
			DockerInteractive: engine.DockerInteractiveRunner(),
			TruenasTunnel:     engine.TruenasTunnelRunner(),
			TruenasDialer:     engine.TruenasUpstreamDialer(),
		}))
	}
	return globalSearchRegistry
}

// executionRouter implements hostexec.Registry using concrete functions.
type executionRouter struct {
	searchReg *searchrun.Registry
}

func (r *executionRouter) ForRecord(rec hosts.Record) hostexec.Executor {
	if ex := r.searchReg.ResolveExecutor(rec, r); ex != nil {
		return ex
	}
	return &sshFallbackExecutor{}
}

func (r *executionRouter) Reconfigure(_ *config.File) {
	r.searchReg.ReconfigureFromConfig()
}

func (r *executionRouter) RunSSHTunnel(ctx context.Context, user, host string, sshPort int, localFwd string, out io.Writer) error {
	return sshclient.RunTunnelGo(ctx, user, host, sshPort, localFwd, out)
}

func (r *executionRouter) BorrowSSH(_ string, _ hosts.Record) (any, bool) {
	return nil, false // Not implemented globally by default
}

type sshFallbackExecutor struct{}

// sshFallbackExecutor is the seam's interactive path for plain SSH records, so a
// web/TUI terminal resolves it through Registry.ForRecord + InteractiveStreamer
// like docker/k8s instead of the caller inlining raw ssh.Session plumbing.
var _ hostexec.InteractiveStreamer = (*sshFallbackExecutor)(nil)

func (e *sshFallbackExecutor) Dial(user string, r hosts.Record) (hostexec.HostClient, error) {
	user = strings.TrimSpace(user)
	if user == "" {
		if u := strings.TrimSpace(r.Meta["ssh_user"]); u != "" {
			user = u
		}
	}
	host := strings.TrimSpace(r.PrimaryIP)
	if host == "" {
		return nil, fmt.Errorf("no host ip for ssh")
	}
	override := 0
	if p, ok := hosts.MetaSSHPort(&r); ok {
		override = p
	}
	identity := ""
	if id, ok := hosts.MetaSSHIdentityFile(&r); ok {
		identity = id
	}
	return sshclient.DialHoneyHost(user, host, override, identity)
}

func (e *sshFallbackExecutor) RunInteractive(user string, r hosts.Record) error {
	user = strings.TrimSpace(user)
	if user == "" {
		if u := strings.TrimSpace(r.Meta["ssh_user"]); u != "" {
			user = u
		}
	}
	return engine.RunSSHInteractive(user, r, nil)
}

// RunInteractiveStreams runs an interactive SSH PTY shell over the caller's
// streams (e.g. a web terminal's WebSocket pipes) instead of os.Stdin/os.Stdout,
// so the executor seam owns the ssh.Session plumbing rather than the handler.
// Under a PTY the remote merges stderr into stdout, so both route to stdout.
// resize carries [cols, rows] pairs, forwarded to the session as WindowChange.
func (e *sshFallbackExecutor) RunInteractiveStreams(ctx context.Context, user string, r hosts.Record, stdin io.Reader, stdout io.Writer, cols, rows int, resize <-chan [2]int) error {
	user = strings.TrimSpace(user)
	if user == "" {
		if u := strings.TrimSpace(r.Meta["ssh_user"]); u != "" {
			user = u
		}
	}
	hc, err := e.Dial(user, r)
	if err != nil {
		return err
	}
	defer func() { _ = hc.Close() }()
	leafer, ok := hc.(interface{ LeafSSH() *ssh.Client })
	if !ok {
		return fmt.Errorf("ssh interactive: client has no leaf")
	}
	leaf := leafer.LeafSSH()
	if leaf == nil {
		return fmt.Errorf("ssh interactive: leaf client unavailable")
	}
	sess, err := leaf.NewSession()
	if err != nil {
		return fmt.Errorf("ssh interactive: new session: %w", err)
	}
	defer func() { _ = sess.Close() }()

	var shellCmd string
	if env, eerr := cuetry.EffectiveEnvForRun(ctx, false, nil, &cuetry.StepBase{}, nil, nil, &r); eerr == nil && len(env) > 0 {
		for k, v := range env {
			_ = sess.Setenv(k, v)
		}
		shellCmd, _ = cuetry.ShellExportPrefixForRemote(env, `exec "${SHELL:-sh}" -l || exec "${SHELL:-sh}"`)
	}

	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := sess.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		return fmt.Errorf("ssh interactive: request pty: %w", err)
	}
	sess.Stdin = stdin
	sess.Stdout = stdout
	sess.Stderr = stdout

	if shellCmd != "" {
		if err := sess.Start(shellCmd); err != nil {
			return fmt.Errorf("ssh interactive: start shell: %w", err)
		}
	} else {
		if err := sess.Shell(); err != nil {
			return fmt.Errorf("ssh interactive: shell: %w", err)
		}
	}

	// Forward terminal resizes until the session ends. done (closed on return)
	// and ctx both stop the goroutine, so it never outlives the session even if
	// the caller never closes resize.
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				_ = sess.Close()
				return
			case sz, ok := <-resize:
				if !ok {
					return
				}
				if sz[0] > 0 && sz[1] > 0 {
					_ = sess.WindowChange(sz[1], sz[0])
				}
			}
		}
	}()

	return sess.Wait()
}

func (e *sshFallbackExecutor) RunTunnel(ctx context.Context, user string, r hosts.Record, localFwd string, out io.Writer) error {
	user = strings.TrimSpace(user)
	if user == "" {
		if u := strings.TrimSpace(r.Meta["ssh_user"]); u != "" {
			user = u
		}
	}
	host := strings.TrimSpace(r.PrimaryIP)
	if host == "" {
		return fmt.Errorf("no host ip for ssh")
	}
	override := 0
	if p, ok := hosts.MetaSSHPort(&r); ok {
		override = p
	}
	return sshclient.RunTunnelGo(ctx, user, host, override, localFwd, out)
}

// sshDialConn couples a dialed SSH channel (net.Conn) with the SSH client that
// owns it, so closing the conn also releases the client — no leaked SSH session.
type sshDialConn struct {
	net.Conn
	closer io.Closer
}

func (c *sshDialConn) Close() error {
	err := c.Conn.Close()
	if c.closer != nil {
		_ = c.closer.Close()
	}
	return err
}

func (e *sshFallbackExecutor) DialUpstream(_ context.Context, user string, r hosts.Record, address string) (net.Conn, error) {
	hc, err := e.Dial(user, r)
	if err != nil {
		return nil, fmt.Errorf("ssh dial for upstream: %w", err)
	}
	leafer, ok := hc.(interface{ LeafSSH() *ssh.Client })
	if !ok {
		_ = hc.Close()
		return nil, fmt.Errorf("ssh client has no leaf for upstream dial")
	}
	leaf := leafer.LeafSSH()
	if leaf == nil {
		_ = hc.Close()
		return nil, fmt.Errorf("ssh leaf client unavailable")
	}
	conn, err := leaf.Dial("tcp", address)
	if err != nil {
		_ = hc.Close()
		return nil, fmt.Errorf("ssh channel dial %s: %w", address, err)
	}
	return &sshDialConn{Conn: conn, closer: hc}, nil
}

// buildHostExecRegistry constructs the host execution registry.
func buildHostExecRegistry() hostexec.Registry {
	return &executionRouter{searchReg: GetSearchRegistry()}
}

// GetExecRegistry returns the host execution registry for SSH/Docker/TrueNAS dispatch.
func GetExecRegistry() hostexec.Registry {
	return buildHostExecRegistry()
}
