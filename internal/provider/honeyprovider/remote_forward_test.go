package honeyprovider

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func writeRFFrameSrv(t *testing.T, conn *websocket.Conn, connID uint32, typ byte, payload []byte) {
	t.Helper()
	frame := make([]byte, rfClientHeaderLen+len(payload))
	binary.BigEndian.PutUint32(frame[0:4], connID)
	frame[4] = typ
	copy(frame[rfClientHeaderLen:], payload)
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, frame))
}

func TestStartRemoteForward(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("github.com/shareed2k/honey/internal/engine.(*GlobalTunnelPool).sweepLoop"))

	// Local target: an echo listener the client should dial for each open frame.
	echoLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var echoWG sync.WaitGroup
	echoWG.Add(1)
	go func() {
		defer echoWG.Done()
		for {
			c, err := echoLn.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()
	localPort := echoLn.Addr().(*net.TCPAddr).Port

	got := make(chan string, 4)

	upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/ws/remote-forward", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		var hello map[string]any
		if err := conn.ReadJSON(&hello); err != nil {
			return
		}
		if err := conn.WriteJSON(map[string]string{"status": "listening", "addr": "1.2.3.4:2222"}); err != nil {
			return
		}

		// Drive a synthetic remote connection: announce it, then send bytes the
		// client must relay to its local target.
		writeRFFrameSrv(t, conn, 1, rfFrameClientOpen, nil)
		writeRFFrameSrv(t, conn, 1, rfFrameClientData, []byte("ping"))

		for {
			mt, p, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if mt != websocket.BinaryMessage || len(p) < rfClientHeaderLen {
				continue
			}
			if p[4] == rfFrameClientData {
				got <- string(p[rfClientHeaderLen:])
			}
		}
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	// Close the listener before waiting so the accept goroutine unblocks.
	defer func() {
		_ = echoLn.Close()
		echoWG.Wait()
	}()

	c := &Client{url: ts.URL, user: "ubuntu"}
	remAddr, stop, err := c.StartRemoteForward(context.Background(), "0.0.0.0", 2222, "127.0.0.1", localPort)
	require.NoError(t, err)
	require.NotNil(t, stop)
	defer stop()
	require.Equal(t, "1.2.3.4:2222", remAddr)

	select {
	case s := <-got:
		require.Equal(t, "ping", s)
	case <-time.After(3 * time.Second):
		t.Fatal("did not receive echoed bytes back over the control WS")
	}

	stop()
}

// ensure the URL scheme rewrite is exercised (http -> ws) without a live server.
func TestStartRemoteForward_DialError(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("github.com/shareed2k/honey/internal/engine.(*GlobalTunnelPool).sweepLoop"))

	c := &Client{url: "http://127.0.0.1:1", user: "ubuntu"}
	_, _, err := c.StartRemoteForward(context.Background(), "0.0.0.0", 2222, "127.0.0.1", 9)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "http://127.0.0.1:1") ||
		strings.Contains(err.Error(), "connect") || strings.Contains(err.Error(), "refused") ||
		strings.Contains(err.Error(), "dial"))
}
