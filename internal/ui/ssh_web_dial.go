package ui

import (
	"context"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/sshclient"
)

// DialSSHLeafForRecord returns a leaf *ssh.Client for SSH to hosts.Record.PrimaryIP (same transport as the TUI).
// Kubernetes pod records are not supported for raw SSH in this helper.
func DialSSHLeafForRecord(user string, r hosts.Record) (*ssh.Client, func(), error) {
	if r.Provider == "k8s" && r.Meta["kind"] == "pod" {
		return nil, nil, fmt.Errorf("web SSH for Kubernetes pods is not supported in this version")
	}
	host := strings.TrimSpace(r.PrimaryIP)
	if host == "" {
		return nil, nil, fmt.Errorf("no IP for selected host")
	}
	sshPort := 0
	if p, ok := hosts.MetaSSHPort(&r); ok {
		sshPort = p
	}
	identity := ""
	if id, ok := hosts.MetaSSHIdentityFile(&r); ok {
		identity = id
	}
	return sshclient.DialSSHClient(user, host, sshPort, identity)
}

// RunSSHInteractiveStreams runs an interactive SSH PTY shell for r over the
// caller's streams (a web terminal's WebSocket pipes, or a recorded session)
// instead of os.Stdin/os.Stdout. It is the universal SSH terminal path: it dials
// the leaf directly via DialSSHLeafForRecord (independent of any executor
// registry), so a plain SSH host always gets a shell. Under a PTY the remote
// merges stderr into stdout, so both route to stdout. resize carries [cols, rows]
// pairs, forwarded to the session as WindowChange; the forwarding goroutine stops
// on session end or ctx cancellation.
//
// It backs both the cli sshFallbackExecutor's InteractiveStreamer seam and the
// webserver's fallback when the resolved executor is not itself interactive, so
// the SSH PTY plumbing lives in exactly one place.
func RunSSHInteractiveStreams(ctx context.Context, user string, r hosts.Record, stdin io.Reader, stdout io.Writer, cols, rows int, resize <-chan [2]int) error {
	client, cleanup, err := DialSSHLeafForRecord(user, r)
	if err != nil {
		return err
	}
	defer cleanup()

	sess, err := client.NewSession()
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
