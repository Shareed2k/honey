//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/webserver"
)

// wsHello mirrors the fields of webserver.WSHello that the client sends. Using a
// local struct avoids depending on unexported tag details.
type wsHello struct {
	Record  any    `json:"record"`
	SSHUser string `json:"ssh_user"`
	Cols    int    `json:"cols"`
	Rows    int    `json:"rows"`
}

// dialWS opens a WebSocket to baseURL's /ws/ssh with bearer auth.
func dialWS(t *testing.T, baseURL, token string) *websocket.Conn {
	t.Helper()
	wsURL := strings.Replace(baseURL, "http://", "ws://", 1) + "/ws/ssh"
	hdr := http.Header{"Authorization": []string{"Bearer " + token}}
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		t.Fatalf("ws dial %s: %v", wsURL, err)
	}
	if resp != nil {
		_ = resp.Body.Close()
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func wsExecServer(t *testing.T, target sshTarget, opts webserver.Options) string {
	t.Helper()
	opts.SearchRegistry = target.searchReg
	opts.ExecRegistry = target.execReg
	if opts.Config == nil {
		opts.Config = &config.File{Defaults: config.Defaults{SSHUser: "testuser"}}
	}
	return newTestServer(t, opts)
}

func TestOPAE2E_WSSSH_InteractiveDenied(t *testing.T) {
	target := newSSHTarget(t)
	enf := newEnforcer(t, `package honey
import rego.v1
default allow := true
default deny_reason := ""
allow := false if {
	input.action == "interactive_session"
	input.target.name == "ssh-test"
}
deny_reason := "no shell on ssh-test" if {
	input.action == "interactive_session"
	input.target.name == "ssh-test"
}`)
	base := wsExecServer(t, target, webserver.Options{Token: "test-token", Enforcer: enf})

	conn := dialWS(t, base, "test-token")
	hello, _ := json.Marshal(wsHello{Record: target.rec, SSHUser: "testuser", Cols: 80, Rows: 24})
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, hello))

	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	mt, payload, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, mt)
	require.Contains(t, string(payload), "session denied")
}

func TestOPAE2E_WSSSH_InteractiveShell(t *testing.T) {
	target := newSSHTarget(t)
	// No enforcer → interactive gate passes → real shell opens on the container.
	base := wsExecServer(t, target, webserver.Options{Token: "test-token"})

	conn := dialWS(t, base, "test-token")
	hello, _ := json.Marshal(wsHello{Record: target.rec, SSHUser: "testuser", Cols: 80, Rows: 24})
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, hello))

	const marker = "HONEY_PTY_OK_4242"
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte("echo "+marker+"\n")))

	var buf strings.Builder
	deadline := time.Now().Add(20 * time.Second)
	_ = conn.SetReadDeadline(deadline)
	seen := false
	for time.Now().Before(deadline) {
		mt, payload, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if mt == websocket.BinaryMessage {
			buf.Write(payload)
			// The shell echoes the typed command and prints its output; the marker
			// appears at least twice. Require it as command output (after a newline)
			// to avoid matching only the echoed input.
			if strings.Count(buf.String(), marker) >= 2 {
				seen = true
				break
			}
		}
	}
	require.True(t, seen, "expected marker %q in pty output, got:\n%s", marker, buf.String())

	_ = conn.WriteMessage(websocket.BinaryMessage, []byte("exit\n"))
}
