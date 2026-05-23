package truenasshell

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type readDeadliner interface {
	Read(p []byte) (n int, err error)
	SetReadDeadline(t time.Time) error
}

func pumpStdinChunk(shell *Session, cp []byte, rec Recorder, exit func(error)) bool {
	detached, werr := StdinChunkToShell(shell, cp, rec)
	if detached {
		exit(nil)
		return true
	}
	if werr != nil {
		exit(werr)
		return true
	}
	return false
}

func pumpStdinBlocking(stdin io.Reader, buf []byte, shell *Session, rec Recorder, exit func(error)) {
	for {
		n, rerr := stdin.Read(buf)
		if n > 0 {
			cp := append([]byte(nil), buf[:n]...)
			if pumpStdinChunk(shell, cp, rec, exit) {
				return
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				exit(nil)
				return
			}
			exit(rerr)
			return
		}
	}
}

func pumpStdinDeadlined(ctx context.Context, stdinDL readDeadliner, buf []byte, readPoll time.Duration, shell *Session, rec Recorder, exit func(error)) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_ = stdinDL.SetReadDeadline(time.Now().Add(readPoll))
		n, rerr := stdinDL.Read(buf)
		if n > 0 {
			cp := append([]byte(nil), buf[:n]...)
			if pumpStdinChunk(shell, cp, rec, exit) {
				return
			}
		}
		if rerr == nil {
			continue
		}
		if errors.Is(rerr, os.ErrDeadlineExceeded) {
			continue
		}
		if rerr == io.EOF {
			exit(nil)
			return
		}
		exit(rerr)
		return
	}
}

func pumpShellToStdoutLoop(shell *Session, stdout io.Writer, rec Recorder, gotOutput *atomic.Bool, exit func(error)) {
	for {
		mt, msg, rerr := shell.ReadMessage()
		if rerr != nil {
			if websocket.IsCloseError(rerr, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				exit(nil)
			} else {
				exit(rerr)
			}
			return
		}
		if rec != nil {
			rec.RecordData("stdout", msg)
		}
		if (mt != websocket.BinaryMessage && mt != websocket.TextMessage) || len(msg) == 0 {
			continue
		}
		gotOutput.Store(true)
		if _, werr := stdout.Write(msg); werr != nil {
			exit(werr)
			return
		}
	}
}

// PumpStdio copies stdin to the TrueNAS shell websocket and shell output to stdout until ctx ends, EOF, or error.
// Terminal resize is handled by the caller via Session.Resize (e.g. SIGWINCH).
func PumpStdio(parentCtx context.Context, shell *Session, stdin io.Reader, stdout io.Writer, rec Recorder) error {
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	errCh := make(chan error, 1)
	var exitOnce sync.Once
	exit := func(err error) {
		exitOnce.Do(func() {
			cancel()
			errCh <- err
		})
	}

	stdinDL, _ := stdin.(readDeadliner)
	const readPoll = 250 * time.Millisecond
	buf := make([]byte, 4096)

	var wg sync.WaitGroup
	wg.Add(2)
	var gotOutput atomic.Bool

	go func() {
		defer wg.Done()
		if stdinDL == nil {
			pumpStdinBlocking(stdin, buf, shell, rec, exit)
			return
		}
		pumpStdinDeadlined(ctx, stdinDL, buf, readPoll, shell, rec, exit)
	}()

	go func() {
		defer wg.Done()
		pumpShellToStdoutLoop(shell, stdout, rec, &gotOutput, exit)
	}()

	waitErr := <-errCh
	if waitErr == nil && !gotOutput.Load() {
		waitErr = fmt.Errorf("truenas shell closed before any output")
	}
	if rec != nil && waitErr != nil {
		rec.RecordError(waitErr)
	}
	_ = shell.writeMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	_ = shell.Close()
	cancel()
	wg.Wait()
	return waitErr
}
