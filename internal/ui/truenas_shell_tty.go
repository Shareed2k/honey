package ui

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/truenasprovider"
	"github.com/shareed2k/honey/internal/sshclient"
	"github.com/shareed2k/honey/internal/truenasshell"
)

func runTrueNASShellInteractive(ctx context.Context, console string, r hosts.Record, recorder *SessionRecorder) error {
	if !truenasshell.ShouldUseTrueNASShell(r, console) {
		return fmt.Errorf("truenas api shell not available for this record")
	}
	b, ok := truenasprovider.BackendByName(r.Meta["backend_name"])
	if !ok {
		return fmt.Errorf("truenas backend not configured")
	}

	fd := int(os.Stdin.Fd())
	if !termIsTerminal(fd) {
		return fmt.Errorf("stdin is not a terminal")
	}
	_, _ = fmt.Fprintf(os.Stderr, "[honey] TrueNAS API shell: press Ctrl+] to leave this session.\n")
	oldState, err := termMakeRaw(fd)
	if err != nil {
		return err
	}
	defer func() { _ = termRestore(fd, oldState) }()

	w, h, err := termGetSize(fd)
	if err != nil {
		w, h = 80, 24
	}

	sess, err := truenasshell.OpenSession(ctx, b, r, h, w)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()

	stopResize := sshclient.StartTerminalResize(fd, func(cols, rows int) {
		if recorder != nil {
			recorder.RecordResize(cols, rows)
		}
		_ = sess.Resize(cols, rows)
	})
	defer stopResize()

	var stdin io.Reader = os.Stdin
	var stdout io.Writer = os.Stdout
	var rec truenasshell.Recorder
	if recorder != nil {
		stdin = WrapRecordingReader(os.Stdin, recorder, "stdin")
		stdout = WrapRecordingWriter(os.Stdout, recorder, "stdout")
		rec = recorder
	}
	return truenasshell.PumpStdio(ctx, sess, stdin, stdout, rec)
}
