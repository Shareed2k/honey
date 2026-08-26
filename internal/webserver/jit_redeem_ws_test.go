package webserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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
//
// shareMuxAvailable is forced off: these tests assert against the in-process
// fakeExecRegistry/fakeInteractiveExecutor, which only the plain-shell
// fallback path (serveWebInteractive) actually drives — the mux path spawns a
// real subprocess (os.Executable() re-exec'd as "pty-proxy"), which in a `go
// test` binary is the TEST BINARY itself, not a real shell. Forcing the
// fallback here exercises exactly what these tests need (and mirrors a real
// host with no tmux); the mux path itself is covered separately by
// TestShareWatch_* and TestPtyMuxBuildShareCommand_ArgvShape, neither of
// which needs a real subprocess spawn.
//
// opts.RecordDir is left as given: recording is now mandatory for a
// redeemed guest session (fail-closed), so a caller whose test needs the
// redeem to actually succeed must set opts.RecordDir (e.g. t.TempDir())
// itself — see TestHandleJITRedeemTerminal_NoRecordDirFailsClosed for the
// case that deliberately leaves it empty.
func newJitWSTestServer(t *testing.T, banner string, opts Options) (*jit.Store, *captureSink, string) {
	t.Helper()
	withShareMuxAvailable(t, false)

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
	store, sink, wsBase := newJitWSTestServer(t, banner, Options{RecordDir: t.TempDir()})
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

// TestHandleJITRedeemTerminal_ShellGrantOversizedFrameRejected is the NEW-11
// regression: the read limit must be set once, right after the upgrade,
// before the very first read — an unauthenticated share-code holder must
// never be able to make honey-web buffer one arbitrarily large frame in its
// heap.
func TestHandleJITRedeemTerminal_ShellGrantOversizedFrameRejected(t *testing.T) {
	store, _, wsBase := newJitWSTestServer(t, "banner", Options{RecordDir: t.TempDir()})
	_, code := createWebGrant(t, store, jit.Grant{Resource: jit.ResourceRef{Name: "web-host", Provider: "ssh"}})

	conn, resp, err := websocket.DefaultDialer.Dial(wsBase+"/api/v1/jit/redeem/"+code+"/terminal", nil)
	require.NoError(t, err)
	defer conn.Close()
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)

	require.NoError(t, conn.WriteJSON(map[string]int{"cols": 80, "rows": 24}))

	// Drain the banner so the shell branch is confirmed up before probing the
	// limit.
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, err = conn.ReadMessage()
	require.NoError(t, err)

	oversized := bytes.Repeat([]byte{'A'}, guestReadLimitBytes+4096)
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, oversized))

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, rerr := conn.ReadMessage()
	require.Error(t, rerr, "a frame over guestReadLimitBytes must close the connection on the plain shell-grant branch too")
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

// TestHandleJITRedeemTerminal_NoRecordDirFailsClosed is the fail-closed
// regression: newWebSessionRecorder silently returns nil when RecordDir is
// empty, which used to hand out an unrecorded, unreplayable guest session.
// The redeem must now be refused instead — and, since the recorder check
// runs BEFORE Jit.Redeem, the one-time code must NOT be consumed, so a
// misconfigured server doesn't burn a guest's single-use link on every
// attempt.
func TestHandleJITRedeemTerminal_NoRecordDirFailsClosed(t *testing.T) {
	store, _, wsBase := newJitWSTestServer(t, "banner", Options{RecordDir: ""})
	created, code := createWebGrant(t, store, jit.Grant{})

	conn, _, err := websocket.DefaultDialer.Dial(wsBase+"/api/v1/jit/redeem/"+code+"/terminal", nil)
	require.NoError(t, err)
	defer conn.Close()

	require.NoError(t, conn.WriteJSON(map[string]int{"cols": 80, "rows": 24}))

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	mt, payload, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, mt)
	require.Contains(t, string(payload), "recording is required")

	got, ok := store.Get(created.ID)
	require.True(t, ok)
	require.Zero(t, got.Redemptions, "a redemption must not be consumed when recording cannot be started")
}
