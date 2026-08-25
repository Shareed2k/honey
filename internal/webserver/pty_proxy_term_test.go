package webserver

import (
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A tmux client honey spawns for a browser terminal must not depend on the
// TERM of whatever started `honey web`: under systemd/docker/launchd there is
// none (or TERM=dumb), and tmux then refuses to attach at all — "open terminal
// failed: terminal does not support clear" — breaking every mux-backed web
// terminal.
func TestWithBrowserTerm_OverridesInheritedTERM(t *testing.T) {
	for _, inherited := range []string{"dumb", "", "xterm"} {
		t.Run("inherited="+inherited, func(t *testing.T) {
			t.Setenv("TERM", inherited)
			t.Setenv("HONEY_TERM_TEST_MARKER", "kept")

			env := withBrowserTerm(exec.Command("tmux", "attach")).Env

			// exec.Cmd resolves duplicate keys to the last one, so the forced
			// value must be the final TERM entry.
			var last string
			for _, kv := range env {
				if strings.HasPrefix(kv, "TERM=") {
					last = kv
				}
			}
			require.Equal(t, "TERM="+browserTermType, last)
			// The rest of the environment must survive: the tmux client still
			// needs PATH, HOME, and honey's own variables.
			require.True(t, slices.Contains(env, "HONEY_TERM_TEST_MARKER=kept"),
				"withBrowserTerm must extend the environment, not replace it")
		})
	}
}

// The operator's read-only watch attach is the path that regressed: it went
// out with no TERM at all.
func TestPtyMuxTmuxWatchAttach_SetsBrowserTERM(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH")
	}
	t.Setenv("TERM", "dumb")

	const name = "honey_term_probe"
	require.NoError(t, exec.Command("tmux", "new-session", "-d", "-s", name, "--", "sh", "-c", "sleep 30").Run())
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", name).Run() })

	cmd, err := ptyMuxTmuxWatchAttach(name)
	require.NoError(t, err)
	require.True(t, slices.Contains(cmd.Env, "TERM="+browserTermType),
		"watch attach must force a usable TERM; got %v", cmd.Env)
}
