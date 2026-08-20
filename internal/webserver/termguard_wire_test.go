package webserver

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/policy"
	"github.com/shareed2k/honey/internal/termguard"
)

// fakeInteractiveStreamer captures whatever bytes it reads from stdin, so a
// test can assert exactly what a wrapped stdin reader delivered downstream.
type fakeInteractiveStreamer struct {
	gotStdin []byte
}

func (f *fakeInteractiveStreamer) RunInteractiveStreams(_ context.Context, _ string, _ hosts.Record, stdin io.Reader, _ io.Writer, _, _ int, _ <-chan [2]int) error {
	b, _ := io.ReadAll(stdin)
	f.gotStdin = b
	return nil
}

// runHandleWebInteractiveStreams drives handleWebInteractiveStreams over a
// real WebSocket loopback: writes payload, then closes the client conn (EOF
// for the fake streamer's io.ReadAll), and waits for the handler to return.
func runHandleWebInteractiveStreams(t *testing.T, guard termGuardInputs, payload []byte) *fakeInteractiveStreamer {
	t.Helper()
	// IgnoreCurrent only snapshots goroutines alive at this call — it cannot
	// see a sibling t.Parallel() test elsewhere in this package spinning up
	// webserver.NewServer (and so NewRecipesAPI's webhook rate limiter/dedup
	// cache) mid-run. Neither has a Shutdown path today, so their pool
	// maintenance loops legitimately outlive any one test; ignore those two
	// known, unrelated background loops by name rather than the whole
	// goroutine set, so a real leak in this test's own code (e.g.
	// termguard/ptyProxyRunBridge goroutines, which are named after their own
	// functions, not ants/ttlcache) still fails it.
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent(),
		goleak.IgnoreTopFunction("github.com/panjf2000/ants/v2.(*poolCommon).ticktock"),
		goleak.IgnoreTopFunction("github.com/panjf2000/ants/v2.(*poolCommon).purgeStaleWorkers"),
		goleak.IgnoreTopFunction("github.com/jellydator/ttlcache/v3.(*Cache[...]).Start"),
	)

	fake := &fakeInteractiveStreamer{}
	upgrader := websocket.Upgrader{}
	done := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		handleWebInteractiveStreams(context.Background(), conn, fake, "user", hosts.Record{Name: "target1"}, 80, 24, nil, guard)
		close(done)
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, payload))
	require.NoError(t, conn.Close())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleWebInteractiveStreams did not return")
	}
	return fake
}

// TestHandleWebInteractiveStreams_GuardOffByteIdentical is the task-P
// regression for the operator terminal wrap point (ws_ssh.go/ws_intercept.go
// share this function): web.guard_mode's default (off) must leave stdin
// completely unwrapped — termguard.NewReader returns the inner reader
// unchanged for ModeOff, so bytes must arrive at the streamer byte-for-byte,
// including a line that WOULD be denied if the guard were active.
func TestHandleWebInteractiveStreams_GuardOffByteIdentical(t *testing.T) {
	t.Parallel()
	payload := []byte("rm -rf /\r")
	fake := runHandleWebInteractiveStreams(t, termGuardInputs{Mode: termguard.ModeOff}, payload)
	require.Equal(t, payload, fake.gotStdin, "guard mode off must forward stdin byte-identical")
}

// TestHandleWebInteractiveStreams_GuardEnforceBlocksOPADeny proves the
// operator wrap point actually gates when OPA denies: with Mode enforce and a
// command_exec-denying policy, the command line's Enter is replaced with a
// Ctrl-U before it ever reaches the InteractiveStreamer.
func TestHandleWebInteractiveStreams_GuardEnforceBlocksOPADeny(t *testing.T) {
	t.Parallel()
	enf, err := policy.NewFromSource(context.Background(), "deny.rego", `package honey
import rego.v1
default allow := true
default deny_reason := ""
allow := false if input.action == "command_exec"
deny_reason := "command_exec blocked by test policy" if input.action == "command_exec"
`)
	require.NoError(t, err)
	fake := runHandleWebInteractiveStreams(t, termGuardInputs{Enforcer: enf, Actor: "alice", Mode: termguard.ModeEnforce}, []byte("rm -rf /\r"))
	require.NotContains(t, string(fake.gotStdin), "\r", "a denied command's Enter must never reach the target")
	require.Contains(t, fake.gotStdin, byte(0x15), "a denied command's Enter must be replaced with Ctrl-U")
}

// TestHandleWebInteractiveStreams_GuardEnforceNilEnforcerAllowsCritical is the
// RM-GUARD consequence regression: OPA is honey's only command-authorization
// gate, so with no enforcer configured, enforce mode blocks nothing at all —
// even a commandrisk-critical line forwards byte-identical to the target.
func TestHandleWebInteractiveStreams_GuardEnforceNilEnforcerAllowsCritical(t *testing.T) {
	t.Parallel()
	payload := []byte("rm -rf /\r")
	fake := runHandleWebInteractiveStreams(t, termGuardInputs{Actor: "alice", Mode: termguard.ModeEnforce}, payload)
	require.Equal(t, payload, fake.gotStdin, "with no OPA enforcer, enforce mode must not block a critical command")
}

// TestHandleWebInteractiveStreams_GuardEnforceAllowsBenign is the allow-path
// counterpart: a benign line passes through the enforcing guard unchanged.
func TestHandleWebInteractiveStreams_GuardEnforceAllowsBenign(t *testing.T) {
	t.Parallel()
	payload := []byte("ls\r")
	fake := runHandleWebInteractiveStreams(t, termGuardInputs{Actor: "alice", Mode: termguard.ModeEnforce}, payload)
	require.Equal(t, payload, fake.gotStdin, "an allowed command must forward byte-identical")
}

// TestNewGuardRelay_deniedAndAllowed exercises newGuardRelay — the
// message-oriented adapter the collaborate-guest relay drives — directly
// against a FAKE decide/onDecision, matching golang-testing's table-driven,
// fake-dependency convention (mirrors internal/termguard's own tests) and
// isolating the frame-adapter mechanics from cmdgate/OPA.
func TestNewGuardRelay_deniedAndAllowed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		input      string
		wantDenied bool
	}{
		{name: "denied line gets Ctrl-U instead of Enter", input: "danger\r", wantDenied: true},
		{name: "allowed line passes through unchanged", input: "safe\r", wantDenied: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var decisions []bool
			decide := func(_ context.Context, cmd string) (string, bool) {
				return "denied by test", strings.Contains(cmd, "danger")
			}
			onDecision := func(_, _ string, denied bool) { decisions = append(decisions, denied) }

			relay, _ := newGuardRelay(context.Background(), io.Discard, termguard.ModeEnforce, decide, onDecision)
			out := relay([]byte(tt.input))

			require.Len(t, out, len(tt.input), "the guard must never change the byte length")
			require.Equal(t, []bool{tt.wantDenied}, decisions)
			if tt.wantDenied {
				require.NotContains(t, string(out), "\r")
				require.Contains(t, out, byte(0x15))
			} else {
				require.Equal(t, tt.input, string(out))
			}
		})
	}
}

// TestNewGuardRelay_multiFrameReconstruction proves a command line split
// across two relay frames still reconstructs into a single decide call —
// the same guarantee termguard.NewReader gives a continuous stream, now
// exercised through the frame-driven adapter.
func TestNewGuardRelay_multiFrameReconstruction(t *testing.T) {
	t.Parallel()
	var seen []string
	decide := func(_ context.Context, cmd string) (string, bool) {
		seen = append(seen, cmd)
		return "", false
	}
	onDecision := func(string, string, bool) {}

	relay, _ := newGuardRelay(context.Background(), io.Discard, termguard.ModeEnforce, decide, onDecision)
	first := relay([]byte("ec"))
	second := relay([]byte("ho hi\r"))

	require.Equal(t, "ec", string(first))
	require.Equal(t, "ho hi\r", string(second))
	require.Equal(t, []string{"echo hi"}, seen, "the line split across two frames must decide exactly once, reconstructed whole")
}

// TestNewGuardRelay_rollbackRestoresPriorSnapshot is the FIX-1(B) regression:
// rollback must restore the guard's line to what it was BEFORE the failed
// frame — never a blind clear. A blind clear discriminates the same as
// rollback ONLY when nothing preceded the failed frame (line was already
// empty); this test establishes REAL, already-relayed content first, so a
// blind clear and a proper rollback produce different, observable results.
func TestNewGuardRelay_rollbackRestoresPriorSnapshot(t *testing.T) {
	t.Parallel()
	var seen []string
	decide := func(_ context.Context, cmd string) (string, bool) {
		seen = append(seen, cmd)
		return "", false
	}
	onDecision := func(string, string, bool) {}

	relay, rollback := newGuardRelay(context.Background(), io.Discard, termguard.ModeEnforce, decide, onDecision)
	_ = relay([]byte("rm -rf ")) // already "relayed" — this is live content
	_ = relay([]byte("poison"))  // this frame is about to fail to relay
	rollback()                   // undo ONLY "poison", not the prior live content
	out := relay([]byte("/\r"))

	require.Equal(t, "/\r", string(out))
	require.Equal(t, []string{"rm -rf /"}, seen, "rollback must restore the PRIOR snapshot (\"rm -rf \"), not wipe the line to empty")
}

// TestNewGuardRelay_rollbackUndoesEnterClear is the denied-Enter variant:
// process() unconditionally clears the line the instant it sees a completed
// line's Enter — BEFORE the caller learns whether the relay carrying that
// Enter will actually succeed. If that frame's relay fails, rollback must
// undo the clear too (not just any appended characters), or a later bare
// Enter decides on an empty line instead of the still-pending denied one —
// exactly the "no decide, no audit, command runs" bug named in review.
func TestNewGuardRelay_rollbackUndoesEnterClear(t *testing.T) {
	t.Parallel()
	var seen []string
	decide := func(_ context.Context, cmd string) (string, bool) {
		seen = append(seen, cmd)
		return "denied by test", true
	}
	onDecision := func(string, string, bool) {}

	relay, rollback := newGuardRelay(context.Background(), io.Discard, termguard.ModeEnforce, decide, onDecision)
	_ = relay([]byte("danger"))
	out1 := relay([]byte("\r")) // denied: Enter -> Ctrl-U, line cleared by process()
	rollback()                  // this whole frame (the Ctrl-U) failed to relay
	out2 := relay([]byte("\r")) // a later bare Enter

	require.Contains(t, out1, byte(0x15), "sanity: the denied Enter must have been replaced with Ctrl-U")
	require.Equal(t, []string{"danger", "danger"}, seen,
		"rollback must restore the pending denied line so a later Enter decides on it again")
	require.Contains(t, out2, byte(0x15), "the later Enter must still be denied, never pass through unaudited")
}

// TestNewGuardRelay_modeOffIsIdentity proves ModeOff is a true, allocation-free
// pass-through (mirrors termguard.NewReader's own contract): decide/onDecision
// must never be invoked, and rollback must be safe to call even though
// nothing was ever built.
func TestNewGuardRelay_modeOffIsIdentity(t *testing.T) {
	t.Parallel()
	decide := func(context.Context, string) (string, bool) {
		t.Fatal("decide must never be called when mode is off")
		return "", false
	}
	onDecision := func(string, string, bool) { t.Fatal("onDecision must never be called when mode is off") }

	relay, rollback := newGuardRelay(context.Background(), io.Discard, termguard.ModeOff, decide, onDecision)
	in := []byte("rm -rf /\r")
	out := relay(in)

	require.Equal(t, in, out)
	require.NotPanics(t, rollback)
}
