package pvelxc

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// readDeadliner is satisfied by *os.File and ui.recordingReader (when inner is *os.File).
type readDeadliner interface {
	Read(p []byte) (n int, err error)
	SetReadDeadline(t time.Time) error
}

func pumpStdioPingLoop(ctx context.Context, done <-chan struct{}, pve *Session) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-t.C:
			if err := pve.WritePing(); err != nil {
				return
			}
		}
	}
}

func pumpStdinBlocking(stdin io.Reader, buf []byte, pve *Session, rec Recorder, exit func(error)) {
	for {
		n, rerr := stdin.Read(buf)
		if n > 0 {
			cp := append([]byte(nil), buf[:n]...)
			if pumpStdinChunk(pve, cp, rec, exit) {
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

func pumpStdinDeadlined(ctx context.Context, stdinDL readDeadliner, buf []byte, readPoll time.Duration, pve *Session, rec Recorder, exit func(error)) {
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
			if pumpStdinChunk(pve, cp, rec, exit) {
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

// pumpStdinChunk forwards one read to PVE; returns true if the session should end (detach or write error).
func pumpStdinChunk(pve *Session, cp []byte, rec Recorder, exit func(error)) bool {
	detached, werr := StdinChunkToPVE(pve, cp, rec)
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

func pumpPVEToStdoutLoop(pve *Session, stdout io.Writer, rec Recorder, exit func(error)) {
	for {
		mt, msg, rerr := pve.ReadMessage()
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
		if _, werr := stdout.Write(msg); werr != nil {
			exit(werr)
			return
		}
	}
}

// PumpStdio copies stdin→PVE and PVE→stdout until ctx is done, an error, or EOF on stdin.
// Terminal resize is handled by the caller via Session.WriteResize (e.g. SIGWINCH).
//
// When the remote side closes the websocket, stdin is unblocked using short read deadlines so
// a background stdin reader cannot keep holding the TTY after the session ends.
func PumpStdio(parentCtx context.Context, pve *Session, stdin io.Reader, stdout io.Writer, rec Recorder) error {
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	done := make(chan struct{})
	defer close(done)

	go pumpStdioPingLoop(ctx, done, pve)

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

	go func() {
		defer wg.Done()
		if stdinDL == nil {
			pumpStdinBlocking(stdin, buf, pve, rec, exit)
			return
		}
		pumpStdinDeadlined(ctx, stdinDL, buf, readPoll, pve, rec, exit)
	}()

	go func() {
		defer wg.Done()
		pumpPVEToStdoutLoop(pve, stdout, rec, exit)
	}()

	waitErr := <-errCh
	if rec != nil && waitErr != nil {
		rec.RecordError(waitErr)
	}
	_ = pve.writeMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	_ = pve.Close()
	cancel()
	wg.Wait()
	return waitErr
}
