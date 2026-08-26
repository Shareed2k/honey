package webserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/truenasshell"
)

func TestWebSSHHelloResolvesDefaultSSHUserForPtyProxy(t *testing.T) {
	t.Parallel()
	s := &Server{opts: Options{Config: &config.File{Defaults: config.Defaults{SSHUser: "ops"}}}}
	helloIn := WSHello{
		SessionID: "abc",
		SSHUser:   "",
		Record:    hosts.Record{Provider: "local", Name: "vm", PrimaryIP: "10.0.0.1"},
		Cols:      80,
		Rows:      24,
	}
	rawIn, err := json.Marshal(helloIn)
	if err != nil {
		t.Fatal(err)
	}
	var hello WSHello
	if err := json.Unmarshal(rawIn, &hello); err != nil {
		t.Fatal(err)
	}
	user := s.sshUser(hello.SSHUser)
	hello.SSHUser = user
	rawOut, err := json.Marshal(hello)
	if err != nil {
		t.Fatal(err)
	}
	var decoded WSHello
	if err := json.Unmarshal(rawOut, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SSHUser != "ops" {
		t.Fatalf("pty-proxy payload ssh_user = %q, want ops", decoded.SSHUser)
	}
	if user != "ops" {
		t.Fatalf("resolved user = %q, want ops", user)
	}
}

func TestShouldUseWebPtyProxy_includesTrueNASAPIConsole(t *testing.T) {
	hello := WSHello{
		SessionID: "tab-1",
		Console:   truenasshell.ConsoleTrueNASAPI,
		Record: hosts.Record{
			Provider: "truenas",
			Name:     "web",
			Meta: map[string]string{
				"kind":         "virt_instance",
				"id":           "abc123",
				"backend_name": "lab",
			},
		},
	}
	if !shouldUseWebPtyProxy(hello) {
		t.Fatal("truenas_api console with session_id should use pty-proxy")
	}

	hello.SessionID = ""
	if shouldUseWebPtyProxy(hello) {
		t.Fatal("expected no pty-proxy without session_id")
	}
}

// TestWsWriter_WriteDeadlineReturnsInsteadOfHanging is the NEW-12 regression:
// a peer that simply stops reading its socket (never closes it, just never
// calls ReadMessage again) must not block wsWriter.Write forever — with no
// deadline this would hold wsWriter's mutex indefinitely, wedging the
// ptmx-reading goroutine in the bridge so bridgeCancel never fires and
// ptyProxyTeardown never runs, leaving a guest's tmux client attached to the
// operator's session forever. wsWriteTimeout is shrunk here so the test
// doesn't wait out the real (generous, operator-facing) 10s default.
func TestWsWriter_WriteDeadlineReturnsInsteadOfHanging(t *testing.T) {
	orig := wsWriteTimeout
	wsWriteTimeout = 100 * time.Millisecond
	t.Cleanup(func() { wsWriteTimeout = orig })

	upgrader := websocket.Upgrader{}
	writerReady := make(chan *wsWriter, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		writerReady <- &wsWriter{conn: conn, mu: &sync.Mutex{}}
		<-r.Context().Done() // keep the connection open; the test drives writes directly
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()
	// Deliberately never call conn.ReadMessage(): this client stops reading,
	// simulating NEW-12's stuck guest (or operator client).

	wsOut := <-writerReady

	chunk := bytes.Repeat([]byte{'a'}, 1<<20) // 1 MiB per write
	done := make(chan error, 1)
	go func() {
		var werr error
		for i := 0; i < 64; i++ { // up to 64 MiB — comfortably overflows any realistic socket buffer
			if _, werr = wsOut.Write(chunk); werr != nil {
				break
			}
		}
		done <- werr
	}()

	select {
	case werr := <-done:
		require.Error(t, werr, "a write to a non-reading peer must eventually fail via the write deadline, not hang forever")
	case <-time.After(5 * time.Second):
		t.Fatal("wsWriter.Write did not return within 5s against a non-reading peer — the write deadline did not fire")
	}
}
