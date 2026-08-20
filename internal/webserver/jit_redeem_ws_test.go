package webserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/jit"
	"github.com/shareed2k/honey/internal/policy"
)

// fakeInteractiveExecutor is a hostexec.Executor + InteractiveStreamer test
// double for the web terminal: RunInteractiveStreams writes a known banner,
// then echoes stdin to stdout until stdin closes or ctx is done, returning nil.
// The plain Executor methods are unused by the interactive path and error.
type fakeInteractiveExecutor struct {
	banner string
}

func (fakeInteractiveExecutor) Dial(string, hosts.Record) (hostexec.HostClient, error) {
	return nil, errors.New("not implemented")
}

func (fakeInteractiveExecutor) RunInteractive(string, hosts.Record) error {
	return errors.New("not implemented")
}

func (fakeInteractiveExecutor) RunTunnel(context.Context, string, hosts.Record, string, io.Writer) error {
	return errors.New("not implemented")
}

func (fakeInteractiveExecutor) DialUpstream(context.Context, string, hosts.Record, string) (net.Conn, error) {
	return nil, errors.New("not implemented")
}

func (f fakeInteractiveExecutor) RunInteractiveStreams(ctx context.Context, _ string, _ hosts.Record, stdin io.Reader, stdout io.Writer, _, _ int, _ <-chan [2]int) error {
	if _, err := io.WriteString(stdout, f.banner); err != nil {
		return err
	}
	buf := make([]byte, 4096)
	for {
		n, err := stdin.Read(buf)
		if n > 0 {
			if _, werr := stdout.Write(buf[:n]); werr != nil {
				return nil
			}
		}
		if err != nil {
			return nil // stdin closed (client left) — clean exit
		}
		select {
		case <-ctx.Done():
			return nil
		default:
		}
	}
}

var (
	_ hostexec.Executor            = fakeInteractiveExecutor{}
	_ hostexec.InteractiveStreamer = fakeInteractiveExecutor{}
)

// fakeExecRegistry is a hostexec.Registry that resolves every record to ex.
type fakeExecRegistry struct {
	ex hostexec.Executor
}

func (f fakeExecRegistry) ForRecord(hosts.Record) hostexec.Executor { return f.ex }
func (fakeExecRegistry) Reconfigure(*config.File)                   {}

func (fakeExecRegistry) RunSSHTunnel(context.Context, string, string, int, string, io.Writer) error {
	return nil
}

func (fakeExecRegistry) BorrowSSH(string, hosts.Record) (any, bool) { return nil, false }

var _ hostexec.Registry = fakeExecRegistry{}

// newJitWSTestServer builds a *Server wired with a jit.Store on a temp file, a
// fake ExecRegistry returning the given interactive streamer, and a capturing
// audit sink, then fronts it with an httptest server. The returned wsBase is
// the ws:// prefix (no path) for dialing redeem terminals.
func newJitWSTestServer(t *testing.T, banner string, opts Options) (*jit.Store, *captureSink, string) {
	t.Helper()
	store, err := jit.NewStore(filepath.Join(t.TempDir(), "jit_grants.jsonl"), nil)
	require.NoError(t, err)

	sink := &captureSink{}
	opts.Jit = store
	opts.AuditSink = sink
	opts.ExecRegistry = fakeExecRegistry{ex: fakeInteractiveExecutor{banner: banner}}

	s := newTestServer(t, opts)

	ts := httptest.NewServer(s.router)
	t.Cleanup(ts.Close)

	wsBase := strings.Replace(ts.URL, "http", "ws", 1)
	return store, sink, wsBase
}

// createWebGrant stores a redeemable web/shell grant and returns it plus its
// one-time plaintext code.
func createWebGrant(t *testing.T, store *jit.Store, g jit.Grant) (jit.Grant, string) {
	t.Helper()
	if g.Delivery == "" {
		g.Delivery = jit.DeliveryWeb
	}
	if len(g.Capabilities) == 0 {
		g.Capabilities = []jit.Capability{jit.CapShell}
	}
	if g.Duration == 0 {
		g.Duration = time.Hour
	}
	if g.Resource.Name == "" {
		g.Resource.Name = "host1"
	}
	if g.Resource.Provider == "" {
		g.Resource.Provider = "ssh"
	}
	created, code, err := store.Create(g)
	require.NoError(t, err)
	return created, code
}

func TestHandleJITRedeemTerminal_UnknownCode(t *testing.T) {
	_, _, wsBase := newJitWSTestServer(t, "banner", Options{})

	conn, resp, err := websocket.DefaultDialer.Dial(wsBase+"/api/v1/jit/redeem/does-not-exist/terminal", nil)
	if conn != nil {
		_ = conn.Close()
	}
	require.Error(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHandleJITRedeemTerminal_CertOnlyGrant404(t *testing.T) {
	store, _, wsBase := newJitWSTestServer(t, "banner", Options{})
	_, code := createWebGrant(t, store, jit.Grant{Delivery: jit.DeliveryCert})

	conn, resp, err := websocket.DefaultDialer.Dial(wsBase+"/api/v1/jit/redeem/"+code+"/terminal", nil)
	if conn != nil {
		_ = conn.Close()
	}
	require.Error(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHandleJITRedeemTerminal_PendingGrant404(t *testing.T) {
	store, _, wsBase := newJitWSTestServer(t, "banner", Options{})
	// RequireApproval => StatusPending, never active until decided.
	_, code := createWebGrant(t, store, jit.Grant{RequireApproval: true})

	conn, resp, err := websocket.DefaultDialer.Dial(wsBase+"/api/v1/jit/redeem/"+code+"/terminal", nil)
	if conn != nil {
		_ = conn.Close()
	}
	require.Error(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHandleJITRedeemTerminal_HappyPath(t *testing.T) {
	const banner = "welcome-to-the-fake-shell\r\n"
	store, sink, wsBase := newJitWSTestServer(t, banner, Options{})
	created, code := createWebGrant(t, store, jit.Grant{Resource: jit.ResourceRef{Name: "web-host", Provider: "ssh"}})

	// goleak baseline snapshot taken now (after NewServer + httptest started);
	// only the handler's per-connection goroutines are checked. Runs before the
	// t.Cleanup(ts.Close) registered in the harness.
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	conn, resp, err := websocket.DefaultDialer.Dial(wsBase+"/api/v1/jit/redeem/"+code+"/terminal", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)

	require.NoError(t, conn.WriteJSON(map[string]int{"cols": 100, "rows": 40}))

	// The fake writes the banner as the first stdout chunk (a BinaryMessage).
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	mt, payload, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.BinaryMessage, mt)
	require.Equal(t, banner, string(payload))

	// A stdin binary frame is echoed straight back.
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte("id\r")))
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	mt, echo, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.BinaryMessage, mt)
	require.Equal(t, "id\r", string(echo))

	// A redemption was consumed (once), and a web-delivery jit_redeemed event
	// was audited — both happen before the terminal is served, so they are
	// observable now.
	got, ok := store.Get(created.ID)
	require.True(t, ok)
	require.Equal(t, 1, got.Redemptions)

	var redeemed bool
	for _, e := range sink.all() {
		if e.Action == "jit_redeemed" {
			redeemed = true
			require.Equal(t, "web", e.Source)
			require.Equal(t, created.ID, e.ApprovalID)
			require.Equal(t, "web-host", e.Target)
			require.Equal(t, "allow", e.Decision)
			require.Equal(t, "web", e.Extra["delivery"])
			require.Equal(t, "share:"+created.ID, e.Actor)
		}
	}
	require.True(t, redeemed, "expected a jit_redeemed audit event")

	// Closing the client lets both terminal goroutines and the handler exit;
	// the deferred goleak.VerifyNone then confirms no leak.
	require.NoError(t, conn.Close())
}

func TestHandleJITRedeemTerminal_OPADeniedOverWS(t *testing.T) {
	const src = `package honey
import rego.v1
default allow := true
default deny_reason := ""
allow := false if { input.action == "interactive_session" }
deny_reason := "no interactive shells for share links" if { input.action == "interactive_session" }`
	enf, err := policy.NewFromSource(context.Background(), "deny.rego", src)
	require.NoError(t, err)

	store, _, wsBase := newJitWSTestServer(t, "banner", Options{Enforcer: enf})
	created, code := createWebGrant(t, store, jit.Grant{})

	conn, _, err := websocket.DefaultDialer.Dial(wsBase+"/api/v1/jit/redeem/"+code+"/terminal", nil)
	require.NoError(t, err)
	defer conn.Close()

	require.NoError(t, conn.WriteJSON(map[string]int{"cols": 80, "rows": 24}))

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	mt, payload, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, mt)
	require.True(t, strings.HasPrefix(string(payload), "session denied:"), "got %q", string(payload))

	// A denied gate must NOT consume a redemption.
	got, ok := store.Get(created.ID)
	require.True(t, ok)
	require.Equal(t, 0, got.Redemptions)
}

func TestHandleJITRedeemTerminal_NoExecRegistry503(t *testing.T) {
	store, err := jit.NewStore(filepath.Join(t.TempDir(), "jit_grants.jsonl"), nil)
	require.NoError(t, err)
	s := newTestServer(t, Options{Jit: store})
	s.opts.ExecRegistry = nil // NewServer may default one; force the unavailable path.

	_, code := createWebGrant(t, store, jit.Grant{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jit/redeem/"+code+"/terminal", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// liveTerminalGrant returns a Grant shaped like the webserver grant-create
// handler's live_terminal output (applyLiveTerminalShare): Meta carries kind +
// mux_session, Capabilities carries exactly the one requested capability.
func liveTerminalGrant(muxSession string, capability jit.Capability) jit.Grant {
	return jit.Grant{
		Resource: jit.ResourceRef{
			Name:     "shared-host",
			Provider: "ssh",
			Meta:     map[string]string{"kind": jitKindLiveTerminal, "mux_session": muxSession},
		},
		Capabilities: []jit.Capability{capability},
	}
}

// TestHandleJITRedeemTerminal_LiveTerminalInvalidMuxSession404 proves the
// missing/invalid mux_session case collapses to the same generic 404 as any
// other bad code (matching the pre-upgrade gate at :49/:53) — decidable
// entirely from the grant, so it never reaches the WebSocket upgrade. Store
// validation only requires mux_session to be non-empty (internal/jit cannot
// import the webserver mux-name validators without a cycle), so "not a real
// honey_*/honey-int-* name" is exactly the gap this handler-level check closes.
func TestHandleJITRedeemTerminal_LiveTerminalInvalidMuxSession404(t *testing.T) {
	store, _, wsBase := newJitWSTestServer(t, "banner", Options{})
	_, code := createWebGrant(t, store, liveTerminalGrant("not-a-real-mux-name", jit.CapWatch))

	conn, resp, err := websocket.DefaultDialer.Dial(wsBase+"/api/v1/jit/redeem/"+code+"/terminal", nil)
	if conn != nil {
		_ = conn.Close()
	}
	require.Error(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestHandleJITRedeemTerminal_LiveTerminalWatchAttach proves the S.3 routing
// end to end against a REAL tmux session standing in for an operator's live
// terminal: a redeemed live_terminal watch grant attaches to that EXACT
// session (never one derived from the client's hello, which here lies about
// session_id/ssh_user/record to prove they are ignored), streams its output,
// and a guest close_tab reaps only the guest's own client — the operator's
// session survives. Skips cleanly when tmux is not on PATH.
func TestHandleJITRedeemTerminal_LiveTerminalWatchAttach(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH; live-terminal attach requires it")
	}

	name := fmt.Sprintf("honey_live_watch_%d", time.Now().UnixNano())
	require.True(t, validHoneyMuxSessionName(name))
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", name).Run() })
	// A trivial producer stands in for the operator's live shell: it proves
	// content flows from the GRANTED session without needing real keystrokes.
	require.NoError(t, exec.Command("tmux", "new-session", "-d", "-s", name, "--",
		"sh", "-c", "while :; do echo tick; sleep 0.05; done").Run())

	store, sink, wsBase := newJitWSTestServer(t, "unused-banner", Options{})
	created, code := createWebGrant(t, store, liveTerminalGrant(name, jit.CapWatch))

	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	conn, resp, err := websocket.DefaultDialer.Dial(wsBase+"/api/v1/jit/redeem/"+code+"/terminal", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)

	// hello.Record/SSHUser/SessionID all lie about the target; the attach must
	// still go to the grant's mux_session, never anything derived from these.
	require.NoError(t, conn.WriteJSON(map[string]any{
		"cols": 80, "rows": 24,
		"session_id": "guest-supplied-should-be-ignored",
		"ssh_user":   "mallory",
		"record":     map[string]any{"name": "someone-elses-host"},
	}))

	sawTick := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !sawTick {
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		mt, payload, rerr := conn.ReadMessage()
		require.NoError(t, rerr)
		if mt == websocket.BinaryMessage && strings.Contains(string(payload), "tick") {
			sawTick = true
		}
	}
	require.True(t, sawTick, "expected to see output from the operator's granted session")

	got, ok := store.Get(created.ID)
	require.True(t, ok)
	require.Equal(t, 1, got.Redemptions)

	var redeemed bool
	for _, e := range sink.all() {
		if e.Action == "jit_redeemed" {
			redeemed = true
			require.Equal(t, "watch", e.Extra["capability"])
		}
	}
	require.True(t, redeemed, "expected a jit_redeemed audit event")

	// Guest close_tab must reap only the guest's own client — never the
	// operator's session.
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"close_tab"}`)))
	require.NoError(t, conn.Close())

	require.Eventually(t, func() bool {
		return exec.Command("tmux", "has-session", "-t", name).Run() == nil
	}, 3*time.Second, 50*time.Millisecond, "operator's session must survive a guest close_tab")
}

// TestHandleJITRedeemTerminal_LiveTerminalCollaborateWritesReachSession proves
// a collaborate grant is read-write end to end: unlike watch, the guest's
// stdin reaches the shared session. The pane runs `cat`, so a write comes
// straight back as output — proof the frame was actually forwarded, not just
// that the socket accepted it. Skips cleanly when tmux is not on PATH.
func TestHandleJITRedeemTerminal_LiveTerminalCollaborateWritesReachSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH; live-terminal attach requires it")
	}

	name := fmt.Sprintf("honey_live_collab_%d", time.Now().UnixNano())
	require.True(t, validHoneyMuxSessionName(name))
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", name).Run() })
	require.NoError(t, exec.Command("tmux", "new-session", "-d", "-s", name, "--", "cat").Run())

	store, _, wsBase := newJitWSTestServer(t, "unused-banner", Options{})
	_, code := createWebGrant(t, store, liveTerminalGrant(name, jit.CapCollab))

	conn, resp, err := websocket.DefaultDialer.Dial(wsBase+"/api/v1/jit/redeem/"+code+"/terminal", nil)
	require.NoError(t, err)
	defer conn.Close()
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)

	require.NoError(t, conn.WriteJSON(map[string]int{"cols": 80, "rows": 24}))
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte("collab-write\r")))

	sawEcho := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !sawEcho {
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		mt, payload, rerr := conn.ReadMessage()
		require.NoError(t, rerr)
		if mt == websocket.BinaryMessage && strings.Contains(string(payload), "collab-write") {
			sawEcho = true
		}
	}
	require.True(t, sawEcho, "a collaborate guest's stdin must reach the shared session")
}
