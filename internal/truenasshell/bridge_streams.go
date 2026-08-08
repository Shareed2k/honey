package truenasshell

import (
	"context"
	"io"
	"sync"

	"github.com/gorilla/websocket"
)

// ttySession is the subset of *Session the stream bridge needs. Declaring it here
// (rather than taking *Session) keeps BridgeStreams unit-testable with a fake in
// place of a live /websocket/shell connection.
type ttySession interface {
	WriteBinary([]byte) error
	Resize(cols, rows int) error
	ReadMessage() (int, []byte, error)
	Close() error
}

// BridgeStreams copies between plain byte streams and an opened TrueNAS shell
// Session — the stream-based analog of BridgeWebSocket used by the SSH gateway's
// interactive console. stdin bytes are forwarded raw (WriteBinary), session output
// is written to stdout, and resize carries [cols, rows] pairs (Resize also wants
// cols, rows). The first goroutine to finish wins; sess is then closed and its
// error returned (nil on a clean EOF or a normal websocket close). TrueNAS runs no
// keepalive, so — unlike the PVE bridge — there is no ping loop.
//
// The output and resize goroutines exit on session close or ctx cancel and are
// joined before return. The stdin reader cannot be interrupted mid-Read, so — like
// BridgeWebSocket, which never waits on its peer-read goroutine — it is not joined:
// it unwinds on its own stdin EOF, or when the caller closes stdin after
// BridgeStreams returns (the SSH gateway closes the channel immediately on return).
//
// When rec is non-nil, stdout/stdin data and the terminating error are recorded.
// The SSH gateway records via its own stdin/stdout wrappers and so passes nil to
// avoid double-recording.
func BridgeStreams(parentCtx context.Context, sess ttySession, stdin io.Reader, stdout io.Writer, resize <-chan [2]int, rec Recorder) error {
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	done := make(chan struct{})

	errCh := make(chan error, 1)
	var exitOnce sync.Once
	exit := func(err error) {
		exitOnce.Do(func() {
			cancel()
			errCh <- err
		})
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		streamOutputLoop(sess, stdout, rec, exit)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		streamResizeLoop(ctx, done, resize, sess)
	}()

	// The stdin reader is deliberately not part of wg (see the doc comment).
	go streamInputLoop(stdin, sess, rec, exit)

	waitErr := <-errCh
	close(done)
	if rec != nil && waitErr != nil {
		rec.RecordError(waitErr)
	}
	_ = sess.Close()
	wg.Wait()
	return waitErr
}

func streamOutputLoop(sess ttySession, stdout io.Writer, rec Recorder, exit func(error)) {
	for {
		mt, msg, rerr := sess.ReadMessage()
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

func streamInputLoop(stdin io.Reader, sess ttySession, rec Recorder, exit func(error)) {
	buf := make([]byte, 32*1024)
	for {
		n, rerr := stdin.Read(buf)
		if n > 0 {
			if rec != nil {
				rec.RecordData("stdin", append([]byte(nil), buf[:n]...))
			}
			if werr := sess.WriteBinary(buf[:n]); werr != nil {
				exit(werr)
				return
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				exit(nil)
			} else {
				exit(rerr)
			}
			return
		}
	}
}

func streamResizeLoop(ctx context.Context, done <-chan struct{}, resize <-chan [2]int, sess ttySession) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case sz, ok := <-resize:
			if !ok {
				return
			}
			// resize carries [cols, rows]; Resize also wants (cols, rows).
			_ = sess.Resize(sz[0], sz[1])
		}
	}
}
