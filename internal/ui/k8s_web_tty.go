package ui

import (
	"context"
	"fmt"
	"io"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hosts"
	"k8s.io/client-go/tools/remotecommand"
)

// ResizeFromColsRows returns a terminal size for k8s remotecommand, or nil if cols or rows are non-positive.
func ResizeFromColsRows(cols, rows int) *remotecommand.TerminalSize {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	return podTermSize(cols, rows)
}

func podTermSize(cols, rows int) *remotecommand.TerminalSize {
	w, h := cols, rows
	if w < 1 {
		w = 80
	}
	if h < 1 {
		h = 24
	}
	if w > 65535 {
		w = 65535
	}
	if h > 65535 {
		h = 65535
	}
	return &remotecommand.TerminalSize{Width: uint16(w), Height: uint16(h)}
}

// ttySizeQueue implements remotecommand.TerminalSizeQueue: first Next returns the
// initial size, then sizes are read from ch until it is closed.
type ttySizeQueue struct {
	initial *remotecommand.TerminalSize
	ch      <-chan *remotecommand.TerminalSize
}

func newTTYSizeQueue(cols, rows int, ch <-chan *remotecommand.TerminalSize) *ttySizeQueue {
	return &ttySizeQueue{
		initial: podTermSize(cols, rows),
		ch:      ch,
	}
}

func (q *ttySizeQueue) Next() *remotecommand.TerminalSize {
	if q.initial != nil {
		s := q.initial
		q.initial = nil
		return s
	}
	if q.ch == nil {
		return nil
	}
	s, ok := <-q.ch
	if !ok || s == nil {
		return nil
	}
	return s
}

// RunK8sPodWebTTY runs an interactive shell in a Kubernetes pod with TTY over stdin/stdout/stderr,
// using the same dial path as the CLI (ephemeral debug container). Resize events are consumed from resizeCh.
func RunK8sPodWebTTY(
	ctx context.Context,
	r hosts.Record,
	stdin io.Reader,
	stdout, stderr io.Writer,
	cols, rows int,
	resizeCh <-chan *remotecommand.TerminalSize,
) error {
	var k engine.K8sPodExecutor
	client, err := k.Dial("", r)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	podClient, ok := client.(*engine.K8sNativeClient)
	if !ok {
		return fmt.Errorf("unexpected client type %T", client)
	}

	env, _ := cuetry.EffectiveEnvForRun(context.Background(), false, nil, &cuetry.StepBase{}, nil, nil, &r)
	shCmd, _ := cuetry.ShellExportPrefixForRemote(env, "sh")
	q := newTTYSizeQueue(cols, rows, resizeCh)
	// Match CLI RunInteractive: pass stderr (often same writer as stdout for web); client-go expects both streams for exec.
	return podClient.ExecInPod(ctx, []string{"sh", "-c", shCmd}, stdin, stdout, stderr, true, q)
}
