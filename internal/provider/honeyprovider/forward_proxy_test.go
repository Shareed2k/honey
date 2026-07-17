package honeyprovider

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	// GlobalTunnelPool starts a process-lifetime background sweep goroutine
	// from a package-level var in internal/engine (imported transitively by
	// this test package via exec_test.go). It has no test-scoped shutdown
	// hook and is unrelated to the code under test here, so it is excluded
	// from the leak check by name rather than ignored wholesale.
	goleak.VerifyTestMain(m,
		goleak.IgnoreTopFunction("github.com/shareed2k/honey/internal/engine.(*GlobalTunnelPool).sweepLoop"))
}

func TestListenAndPipe_LocalForward(t *testing.T) {
	fake := func(_ context.Context, _ string) (net.Conn, error) {
		cli, srv := net.Pipe()
		go func() { io.Copy(srv, srv); srv.Close() }() // echo
		return cli, nil
	}
	host, port, stop, err := listenAndPipe(context.Background(), "127.0.0.1", 0, fake,
		func(net.Conn) (string, error) { return "ignored:0", nil })
	require.NoError(t, err)
	defer stop()
	c, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	require.NoError(t, err)
	defer c.Close()
	_, _ = c.Write([]byte("ping"))
	buf := make([]byte, 4)
	_, err = io.ReadFull(c, buf)
	require.NoError(t, err)
	require.Equal(t, "ping", string(buf))
}

// TestClient_DialUpstream_HandshakeTimeout covers the Important review
// finding on dialUpstream: if the upstream accepts the WS upgrade but then
// stalls before replying to the hello, the post-upgrade handshake (WriteJSON
// + ReadJSON) must be bounded by upstreamHandshakeTimeout instead of blocking
// forever (which would hang listenAndPipe's per-conn goroutine, and in turn
// stop()'s wg.Wait()).
func TestClient_DialUpstream_HandshakeTimeout(t *testing.T) {
	orig := upstreamHandshakeTimeout
	upstreamHandshakeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { upstreamHandshakeTimeout = orig })

	upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/ws/tunnel", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// Accept the upgrade but never reply to the hello: simulates an
		// upstream that stalls after a successful WS handshake.
		_, _, _ = conn.ReadMessage()
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := &Client{url: ts.URL, user: "ubuntu"}

	type result struct {
		err error
	}
	done := make(chan result, 1)
	go func() {
		_, err := c.dialUpstream(context.Background(), "x:1")
		done <- result{err: err}
	}()

	select {
	case res := <-done:
		require.Error(t, res.err)
	case <-time.After(time.Second):
		t.Fatal("dialUpstream did not return within 1s; handshake is not bounded by a deadline")
	}
}
