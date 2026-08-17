package webserver

import (
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestInterceptResumeTmuxLifecycle exercises the real Task-5 resume helpers
// (interceptResumeSetMeta, tmuxListHoneyIntercept, interceptResumeCloseTabKill
// — the × teardown kill, which wraps interceptResumeStop)
// against a REAL tmux server — the part of the browser-refresh-resume feature
// that is unique to this feature and not already covered by
// TestTmuxListHoneyIntercept/TestInterceptResumeStop's canned-output unit
// tests in intercept_mux_test.go.
//
// It does NOT spin up a cluster or run a real interception: the pane runs a
// trivial long-lived command (cat) instead of `honey intercept-pane`, so this
// proves the tmux SESSION lifecycle (create detached, list with metadata,
// survive with no attached client, Stop tears it down) without any of the
// heavier machinery a full resume e2e would need.
//
// Skips cleanly when tmux is not on PATH (e.g. some CI runners); the release
// image always ships tmux (see Dockerfile.release), so this only skips in
// environments that never run the resume path anyway.
func TestInterceptResumeTmuxLifecycle(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH; resume-path tests require it (release image ships tmux)")
	}

	// A fresh, unlikely-to-collide name so this test never trips over a
	// leftover or concurrently-running honey-int-* session on the host.
	pod := fmt.Sprintf("resume-test-pod-%d", time.Now().UnixNano())
	const namespace = "resume-test-ns"
	const cluster = "resume-test-cluster"
	// Leading '-' on purpose: actor is a free-form SSO field, and the metadata
	// writes pass "--" so tmux cannot mistake the value for a flag.
	const actor = "-resume-test-actor"
	const modeCSV = "egress"
	name := interceptPaneMuxName(cluster, namespace, pod)
	require.True(t, validInterceptMuxName(name), "generated mux name must pass the resume-path validator")

	// Safety net: unconditionally kill the session on exit, even on an
	// assertion failure mid-test, so this never leaks a tmux session. name is
	// a hex digest from interceptPaneMuxName (validated above), never a raw
	// or attacker-influenced string.
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", name).Run() })

	// Create the session already detached (-d): no client ever attaches, the
	// same shape a resumed pane is in between browser visits. `cat` is the
	// trivial long-lived pane command — not a real intercept.
	createArgs := []string{"new-session", "-d", "-s", name, "--", "cat"}
	out, err := exec.Command("tmux", createArgs...).CombinedOutput()
	require.NoErrorf(t, err, "tmux new-session: %s", out)

	// Write metadata through the real helper (not a mocked tmuxRun).
	interceptResumeSetMeta(name, pod, namespace, cluster, actor, modeCSV)

	// tmuxListHoneyIntercept must surface the session with its metadata,
	// proving list mechanics + the set-environment round trip work end to end
	// against real tmux.
	infos := tmuxListHoneyIntercept()
	var found *interceptSessionInfo
	for i := range infos {
		if infos[i].Name == name {
			found = &infos[i]
			break
		}
	}
	require.NotNil(t, found, "tmuxListHoneyIntercept must list the freshly created session")
	require.Equal(t, pod, found.Pod)
	require.Equal(t, namespace, found.Namespace)
	require.Equal(t, cluster, found.Cluster)
	require.Equal(t, actor, found.Actor)
	require.Equal(t, modeCSV, found.Mode)
	require.False(t, found.StartedAt.IsZero(), "HONEY_INT_STARTED must round-trip")

	view := found.view()
	require.Equal(t, name, view.ID)
	require.Equal(t, []string{"egress"}, view.Modes)

	// "Detach" — no client ever attached to this session — must not end it:
	// it has zero attached clients yet is still alive and still listed.
	clientsOut, _ := exec.Command("tmux", "list-clients", "-t", name).Output()
	require.Empty(t, clientsOut, "session must have no attached clients")
	require.NoError(t, exec.Command("tmux", "has-session", "-t", name).Run(), "detached session must still be alive")

	// A re-write on attach must not clobber the original metadata (it would
	// reset the start time), so a second call with a different actor is a no-op.
	interceptResumeSetMeta(name, pod, namespace, cluster, "someone-else", modeCSV)
	envOut, err := exec.Command("tmux", "show-environment", "-t", name).Output()
	require.NoError(t, err)
	require.Equal(t, actor, parseTmuxEnvironment(string(envOut))["HONEY_INT_ACTOR"], "attach must not rewrite metadata")

	// The × (close_tab) teardown kill must kill the session — the honey_* mux
	// killers ptyProxyTeardown reaches for the SSH path are inert for
	// honey-int-* names, which is why the resume path passes this one.
	interceptResumeCloseTabKill(name)()

	// ...so has-session now fails and it drops out of the resume list.
	require.Error(t, exec.Command("tmux", "has-session", "-t", name).Run(), "session must be gone after close_tab")
	for _, si := range tmuxListHoneyIntercept() {
		require.NotEqual(t, name, si.Name, "stopped session must not still be listed")
	}
}
