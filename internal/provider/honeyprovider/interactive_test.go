package honeyprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/stretchr/testify/require"
)

// TestRunInteractiveWS drives runInteractiveWS (the testable core behind
// Executor.RunInteractive) against an httptest WS server that speaks the
// /ws/ssh protocol: read the hello, echo BinaryMessages back, record any
// resize TextMessage, and reply with {"closed":true} when it sees a sentinel
// binary payload. No real TTY is involved: in/out are in-memory io.Pipes.
func TestRunInteractiveWS(t *testing.T) {
	type resizeMsg struct {
		Type string `json:"type"`
		Cols int    `json:"cols"`
		Rows int    `json:"rows"`
	}

	sentinel := []byte("__CLOSE_SESSION__")
	resizeReceived := make(chan resizeMsg, 1)
	helloReceived := make(chan map[string]any, 1)

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/ssh", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		_, helloRaw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var hello map[string]any
		if err := json.Unmarshal(helloRaw, &hello); err != nil {
			return
		}
		helloReceived <- hello

		for {
			mt, p, err := conn.ReadMessage()
			if err != nil {
				return
			}
			switch mt {
			case websocket.BinaryMessage:
				if bytes.Equal(p, sentinel) {
					_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"closed":true}`))
					return
				}
				_ = conn.WriteMessage(websocket.BinaryMessage, p)
			case websocket.TextMessage:
				var rz resizeMsg
				if json.Unmarshal(p, &rz) == nil && rz.Type == "resize" {
					select {
					case resizeReceived <- rz:
					default:
					}
				}
			}
		}
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	resizeCh := make(chan [2]int, 1)

	hello := map[string]any{
		"ssh_user": "ubuntu",
		"record":   hosts.Record{Name: "test-host"},
		"cols":     80,
		"rows":     24,
	}

	runDone := make(chan error, 1)
	go func() {
		runDone <- runInteractiveWS(context.Background(), ts.URL, hello, inR, outW, "", nil, nil, resizeCh)
	}()

	select {
	case gotHello := <-helloReceived:
		require.Equal(t, "ubuntu", gotHello["ssh_user"])
	case <-time.After(2 * time.Second):
		t.Fatal("server did not receive hello")
	}

	// Bytes written to `in` must echo back through `out`.
	payload := []byte("hello from client stdin")
	go func() { _, _ = inW.Write(payload) }()
	got := make([]byte, len(payload))
	_, err := io.ReadFull(outR, got)
	require.NoError(t, err)
	require.Equal(t, payload, got)

	// A resize sent on the resize chan must reach the server as the right
	// TextMessage.
	resizeCh <- [2]int{132, 43}
	select {
	case rz := <-resizeReceived:
		require.Equal(t, 132, rz.Cols)
		require.Equal(t, 43, rz.Rows)
	case <-time.After(2 * time.Second):
		t.Fatal("resize message did not reach server")
	}

	// A sentinel payload makes the fake server send {"closed":true}, which the
	// reader goroutine turns into readDone <- nil; runInteractiveWS returns
	// promptly on that signal alone (it does not wait on the stdin-pump
	// goroutine -- see runInteractiveWS's doc comment). Closing the input
	// pipe writer afterward is for this test's own hygiene, not for
	// runInteractiveWS's return: it lets the stdin-pump goroutine observe EOF
	// and exit instead of sitting blocked in inR.Read forever, which would
	// otherwise trip the package's goleak.VerifyTestMain at process exit.
	go func() {
		_, _ = inW.Write(sentinel)
		_ = inW.Close()
	}()

	select {
	case err := <-runDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("runInteractiveWS did not return after the server closed the session")
	}

	_ = outR.Close()
}

// TestRunInteractiveStreams drives the exported Executor.RunInteractiveStreams
// (the entry the web terminal uses for upstream-proxied records) against an
// httptest /ws/ssh server: it verifies the Executor builds the hello with the
// record + given size, round-trips stdin->stdout, and returns cleanly when the
// server closes the session on a sentinel.
func TestRunInteractiveStreams(t *testing.T) {
	sentinel := []byte("__CLOSE_SESSION__")
	helloReceived := make(chan map[string]any, 1)

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/ssh", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, helloRaw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var hello map[string]any
		if err := json.Unmarshal(helloRaw, &hello); err != nil {
			return
		}
		helloReceived <- hello
		for {
			mt, p, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if mt == websocket.BinaryMessage {
				if bytes.Equal(p, sentinel) {
					_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"closed":true}`))
					return
				}
				_ = conn.WriteMessage(websocket.BinaryMessage, p)
			}
		}
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	resizeCh := make(chan [2]int, 1)

	e := &Executor{URL: ts.URL}
	rec := hosts.Record{Name: "web-host", Meta: map[string]string{"kind": "docker", "honey_upstream_backend": "dokploy"}}

	runDone := make(chan error, 1)
	go func() {
		runDone <- e.RunInteractiveStreams(context.Background(), "ubuntu", rec, inR, outW, 90, 30, resizeCh)
	}()

	select {
	case h := <-helloReceived:
		require.Equal(t, "ubuntu", h["ssh_user"])
		require.EqualValues(t, 90, h["cols"])
		require.EqualValues(t, 30, h["rows"])
	case <-time.After(2 * time.Second):
		t.Fatal("server did not receive hello")
	}

	payload := []byte("web stdin bytes")
	go func() { _, _ = inW.Write(payload) }()
	got := make([]byte, len(payload))
	_, err := io.ReadFull(outR, got)
	require.NoError(t, err)
	require.Equal(t, payload, got)

	go func() {
		_, _ = inW.Write(sentinel)
		_ = inW.Close()
	}()
	select {
	case err := <-runDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("RunInteractiveStreams did not return after session close")
	}
	_ = outR.Close()
}
