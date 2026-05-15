package ui

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/pvelxc"
	"github.com/shareed2k/honey/internal/sshclient"
)

func init() {
	hostexec.SetK8sExecutor(&k8sPodExecutor{})
	hostexec.SetSSHRunInteractive(func(user string, r hosts.Record, rec any) error {
		var sr *SessionRecorder
		if rec != nil {
			sr, _ = rec.(*SessionRecorder)
		}
		return runSSHInteractive(user, r, sr)
	})
}

// RunTerminalInteractive opens an interactive session (SSH, K8s, or Proxmox) on os.Stdin/Stdout.
func RunTerminalInteractive(user string, r hosts.Record) error {
	if r.Provider == "k8s" && r.Meta["kind"] == "pod" {
		return runK8sInteractiveWithRecorder(user, r, nil)
	}
	return runSSHInteractive(user, r, nil)
}

// runSSHInteractive opens a login shell over crypto/ssh (respects ~/.ssh/config),
// or a Proxmox LXC/QEMU serial PVE console when exec_mode/token match the same policy as the web UI.
func runSSHInteractive(user string, r hosts.Record, recorder *SessionRecorder) error {
	if pvelxc.ShouldUsePVETTY(r) {
		return runProxmoxLXCTTYInteractive(context.Background(), r, recorder)
	}
	host := r.PrimaryIP
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("no IP for selected host")
	}
	client, cleanup, err := sshclient.DialSSHClient(user, host)
	if err != nil {
		return err
	}
	defer cleanup()

	return runSSHTerminal(client, r, recorder)
}

func runProxmoxLXCTTYInteractive(ctx context.Context, r hosts.Record, recorder *SessionRecorder) error {
	b, ok := hostexec.ProxmoxBackendByName(r.Meta["backend_name"])
	if !ok {
		return fmt.Errorf("proxmox backend not configured")
	}
	node := strings.TrimSpace(r.Meta["node"])
	vmid, err := strconv.Atoi(strings.TrimSpace(r.Meta["vmid"]))
	if err != nil || vmid <= 0 || node == "" {
		return fmt.Errorf("proxmox record missing node or vmid")
	}

	fd := int(os.Stdin.Fd())
	if !termIsTerminal(fd) {
		return fmt.Errorf("stdin is not a terminal")
	}
	_, _ = fmt.Fprintf(os.Stderr, "[honey] Proxmox console: press Ctrl+] to leave this session (guest autologin on tty may reopen a shell after exit).\n")
	oldState, err := termMakeRaw(fd)
	if err != nil {
		return err
	}
	defer func() { _ = termRestore(fd, oldState) }()

	w, h, err := termGetSize(fd)
	if err != nil {
		w, h = 80, 24
	}

	guest := strings.TrimSpace(r.Meta["kind"])
	if guest == "" {
		guest = "lxc"
	}
	sess, err := pvelxc.OpenSession(ctx, b, guest, node, vmid, h, w)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()

	stopResize := sshclient.StartTerminalResize(fd, func(cols, rows int) {
		if recorder != nil {
			recorder.RecordResize(cols, rows)
		}
		_ = sess.WriteResize(rows, cols)
	})
	defer stopResize()

	var stdin io.Reader = os.Stdin
	var stdout io.Writer = os.Stdout
	var rec pvelxc.Recorder
	if recorder != nil {
		stdin = WrapRecordingReader(os.Stdin, recorder, "stdin")
		stdout = WrapRecordingWriter(os.Stdout, recorder, "stdout")
		rec = recorder
	}
	return pvelxc.PumpStdio(ctx, sess, stdin, stdout, rec)
}

func runSSHTerminal(client *ssh.Client, r hosts.Record, recorder *SessionRecorder) error {
	fd := int(os.Stdin.Fd())
	if !termIsTerminal(fd) {
		return fmt.Errorf("stdin is not a terminal")
	}
	oldState, err := termMakeRaw(fd)
	if err != nil {
		return err
	}
	defer func() { _ = termRestore(fd, oldState) }()

	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()

	var shellCmd string
	env, err := cuetry.EffectiveEnvForRun(cuetry.RecipeStep{}, nil, nil, &r)
	if err == nil && len(env) > 0 {
		for k, v := range env {
			_ = sess.Setenv(k, v)
		}
		shellCmd, _ = cuetry.ShellExportPrefixForRemote(env, `exec "${SHELL:-sh}" -l || exec "${SHELL:-sh}"`)
	}

	w, h, err := termGetSize(fd)
	if err != nil {
		w, h = 80, 24
	}
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := sess.RequestPty("xterm-256color", h, w, modes); err != nil {
		return err
	}
	var stdin io.Reader = os.Stdin
	var stdout, stderr io.Writer = os.Stdout, os.Stderr
	if recorder != nil {
		stdin = WrapRecordingReader(os.Stdin, recorder, "stdin")
		stdout = WrapRecordingWriter(os.Stdout, recorder, "stdout")
		stderr = WrapRecordingWriter(os.Stderr, recorder, "stderr")
	}
	sess.Stdin = stdin
	sess.Stdout = stdout
	sess.Stderr = stderr

	var stopResize func()
	if recorder != nil {
		stopResize = sshclient.StartPTYResizeForwarding(fd, sess, func(cols, rows int) {
			recorder.RecordResize(cols, rows)
		})
	} else {
		stopResize = sshclient.StartPTYResizeForwarding(fd, sess, nil)
	}
	defer stopResize()

	if shellCmd != "" {
		if err := sess.Start(shellCmd); err != nil {
			if recorder != nil {
				recorder.RecordError(err)
			}
			return err
		}
	} else {
		if err := sess.Shell(); err != nil {
			if recorder != nil {
				recorder.RecordError(err)
			}
			return err
		}
	}
	waitErr := sess.Wait()
	if waitErr != nil && recorder != nil {
		recorder.RecordError(waitErr)
	}
	return waitErr
}
