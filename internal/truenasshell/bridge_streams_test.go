package truenasshell

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
	"go.uber.org/goleak"
)

// fakeTTYSession is a scripted stand-in for *Session implementing the local
// ttySession interface. ReadMessage yields each queued output frame, then blocks
// until Close so the session stays "open" while stdin and resize are exercised —
// the bridge then ends the moment the test closes the stdin pipe (EOF).
// inputSig/resizeSig let the test await each side effect deterministically.
type fakeTTYSession struct {
	mu      sync.Mutex
	outputs [][]byte
	readIdx int
	inputs  []byte
	resizes [][2]int // recorded as Resize received them: {cols, rows}
	closed  bool

	closeCh   chan struct{}
	closeOnce sync.Once
	inputSig  chan struct{}
	resizeSig chan struct{}
}

func newFakeTTYSession(outputs ...[]byte) *fakeTTYSession {
	return &fakeTTYSession{
		outputs:   outputs,
		closeCh:   make(chan struct{}),
		inputSig:  make(chan struct{}, 1),
		resizeSig: make(chan struct{}, 1),
	}
}

func (f *fakeTTYSession) WriteBinary(p []byte) error {
	f.mu.Lock()
	f.inputs = append(f.inputs, p...)
	f.mu.Unlock()
	select {
	case f.inputSig <- struct{}{}:
	default:
	}
	return nil
}

func (f *fakeTTYSession) Resize(cols, rows int) error {
	f.mu.Lock()
	f.resizes = append(f.resizes, [2]int{cols, rows})
	f.mu.Unlock()
	select {
	case f.resizeSig <- struct{}{}:
	default:
	}
	return nil
}

func (f *fakeTTYSession) ReadMessage() (int, []byte, error) {
	f.mu.Lock()
	if f.readIdx < len(f.outputs) {
		p := f.outputs[f.readIdx]
		f.readIdx++
		f.mu.Unlock()
		return websocket.BinaryMessage, p, nil
	}
	f.mu.Unlock()
	<-f.closeCh
	return 0, nil, io.EOF
}

func (f *fakeTTYSession) Close() error {
	f.closeOnce.Do(func() {
		f.mu.Lock()
		f.closed = true
		f.mu.Unlock()
		close(f.closeCh)
	})
	return nil
}

func (f *fakeTTYSession) inputBytes() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.inputs...)
}

func (f *fakeTTYSession) lastResize() ([2]int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.resizes) == 0 {
		return [2]int{}, false
	}
	return f.resizes[len(f.resizes)-1], true
}

func (f *fakeTTYSession) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func TestBridgeStreams(t *testing.T) {
	defer goleak.VerifyNone(t)

	fake := newFakeTTYSession([]byte("shell-output"))
	pr, pw := io.Pipe()
	var out bytes.Buffer
	resize := make(chan [2]int, 1)

	errc := make(chan error, 1)
	go func() {
		errc <- BridgeStreams(context.Background(), fake, pr, &out, resize, nil)
	}()

	// stdin -> session (io.Pipe write blocks until the input loop reads it).
	if _, err := pw.Write([]byte("client-input")); err != nil {
		t.Fatalf("pipe write: %v", err)
	}
	<-fake.inputSig

	// resize carries [cols, rows]; Resize must receive (cols, rows) unchanged.
	resize <- [2]int{100, 40}
	<-fake.resizeSig

	// EOF on stdin ends the session cleanly.
	_ = pw.Close()

	if err := <-errc; err != nil {
		t.Fatalf("BridgeStreams returned %v, want nil on clean EOF", err)
	}

	if got := out.String(); got != "shell-output" {
		t.Fatalf("stdout = %q, want %q", got, "shell-output")
	}
	if got := string(fake.inputBytes()); got != "client-input" {
		t.Fatalf("session input = %q, want %q", got, "client-input")
	}
	rz, ok := fake.lastResize()
	if !ok {
		t.Fatalf("no resize forwarded to session")
	}
	if rz != [2]int{100, 40} {
		t.Fatalf("Resize got {cols,rows}=%v, want {100,40}", rz)
	}
	if !fake.isClosed() {
		t.Fatalf("session Close was not called")
	}
}
