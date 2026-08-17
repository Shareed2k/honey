package webserver

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// swapTmuxRun installs a fake tmux runner and returns a restore func. Tests that
// use it must NOT run in parallel — tmuxRun is a package var.
func swapTmuxRun(fn func(...string) ([]byte, error)) func() {
	orig := tmuxRun
	tmuxRun = fn
	return func() { tmuxRun = orig }
}

func TestValidInterceptMuxName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"valid hex", "honey-int-0123456789abcdef", true},
		{"valid short suffix", "honey-int-a", true},
		{"honey_ prefix rejected", "honey_deadbeef", false},
		{"no prefix", "main", false},
		{"empty", "", false},
		{"prefix only, no suffix", "honey-int-", false},
		{"uppercase", "honey-int-ABCDEF", false},
		{"slash", "honey-int-a/b", false},
		{"dot", "honey-int-a.b", false},
		{"underscore", "honey-int-a_b", false},
		{"too long", "honey-int-" + strings.Repeat("a", 65), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, validInterceptMuxName(tc.in))
		})
	}
}

func TestParseTmuxEnvironment(t *testing.T) {
	t.Parallel()
	out := "HONEY_INT_POD=pod-a\nHONEY_INT_NS=ns-a\n-REMOVED_VAR\nWEIRD_NO_EQ\nEMPTY=\nWITH=eq=signs\n"
	env := parseTmuxEnvironment(out)
	require.Equal(t, "pod-a", env["HONEY_INT_POD"])
	require.Equal(t, "ns-a", env["HONEY_INT_NS"])
	require.Equal(t, "", env["EMPTY"])
	require.Equal(t, "eq=signs", env["WITH"], "split on the first = only")
	_, hasRemoved := env["REMOVED_VAR"]
	require.False(t, hasRemoved, "lines starting with - (removed vars) are skipped")
	_, hasWeird := env["WEIRD_NO_EQ"]
	require.False(t, hasWeird, "lines without = are skipped")
}

func TestTmuxListHoneyIntercept(t *testing.T) {
	restore := swapTmuxRun(func(args ...string) ([]byte, error) {
		switch args[0] {
		case "list-sessions":
			// Mix of honey-int-, honey_ (SSH), and unrelated sessions.
			return []byte("honey-int-aaaaaaaaaaaaaaaa\nhoney_other\nmain\nhoney-int-bbbbbbbbbbbbbbbb\n"), nil
		case "show-environment":
			switch args[2] {
			case "honey-int-aaaaaaaaaaaaaaaa":
				return []byte("HONEY_INT_POD=pod-a\nHONEY_INT_NS=ns-a\nHONEY_INT_CLUSTER=c1\nHONEY_INT_ACTOR=alice\nHONEY_INT_MODE=egress,incoming\nHONEY_INT_STARTED=2026-08-17T10:00:00Z\nOTHER=x\n"), nil
			case "honey-int-bbbbbbbbbbbbbbbb":
				return []byte("HONEY_INT_POD=pod-b\n"), nil
			}
		}
		return nil, fmt.Errorf("unexpected tmux args %v", args)
	})
	defer restore()

	infos := tmuxListHoneyIntercept()
	require.Len(t, infos, 2, "only honey-int-* sessions are kept")

	byName := map[string]interceptSessionInfo{}
	for _, si := range infos {
		byName[si.Name] = si
	}

	a := byName["honey-int-aaaaaaaaaaaaaaaa"]
	require.Equal(t, "pod-a", a.Pod)
	require.Equal(t, "ns-a", a.Namespace)
	require.Equal(t, "c1", a.Cluster)
	require.Equal(t, "alice", a.Actor)
	require.Equal(t, "egress,incoming", a.Mode)
	require.False(t, a.StartedAt.IsZero(), "RFC3339 HONEY_INT_STARTED parses")

	v := a.view()
	require.Equal(t, "honey-int-aaaaaaaaaaaaaaaa", v.ID)
	require.Equal(t, []string{"egress", "incoming"}, v.Modes)
	require.Equal(t, "pod-a", v.Pod)

	// b has only a pod set; the rest are empty and StartedAt is zero.
	b := byName["honey-int-bbbbbbbbbbbbbbbb"]
	require.Equal(t, "pod-b", b.Pod)
	require.Empty(t, b.Cluster)
	require.True(t, b.StartedAt.IsZero())
}

func TestInterceptResumeStop(t *testing.T) {
	var gotArgs []string
	restore := swapTmuxRun(func(args ...string) ([]byte, error) {
		gotArgs = args
		return nil, nil
	})
	defer restore()

	require.NoError(t, interceptResumeStop("honey-int-0123456789abcdef"))
	require.Equal(t, []string{"kill-session", "-t", "honey-int-0123456789abcdef"}, gotArgs)

	// An invalid name must never reach the exec argv.
	gotArgs = nil
	require.Error(t, interceptResumeStop("honey_bad"))
	require.Nil(t, gotArgs, "kill-session must not run for an invalid name")
}
