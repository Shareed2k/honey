package webserver

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"go.uber.org/zap"
)

// interceptMuxPrefix names the tmux sessions that host web-interception resume
// panes (honey-int-<hex>, see interceptPaneMuxName). The honey_* mux helpers in
// pty_mux.go gate on validHoneyMuxSessionName and are INERT for this family, so
// list/cap/stop for the resume path live here with their own validator.
const interceptMuxPrefix = "honey-int-"

// tmuxRun executes tmux and returns its stdout. It is a package var so unit
// tests can feed canned output without a real tmux on PATH. Every caller
// validates any session name via validInterceptMuxName before it reaches argv,
// and the subcommand verbs are fixed literals — the args are never
// shell-interpreted (exec.Command runs no shell).
var tmuxRun = func(args ...string) ([]byte, error) {
	// CombinedOutput (not Output) so a non-zero exit carries tmux's stderr
	// message (e.g. "can't find session") into the returned error, instead of a
	// bare "exit status 1". On success stderr is empty, so parsers that read the
	// bytes as stdout are unaffected.
	return exec.Command("tmux", args...).CombinedOutput() // #nosec G204 -- fixed tmux verbs + names validated by validInterceptMuxName; no shell
}

// tmuxOnPath reports whether tmux is available. The intercept resume path is
// tmux-only (its list/cap/stop are tmux-based, so a zellij-hosted pane could
// not be managed), so the gate checks tmux specifically, not ptyMuxAvailable.
func tmuxOnPath() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// validInterceptMuxName reports whether name is a safe honey-int-* resume
// session id: the literal prefix, a non-empty suffix, only [a-z0-9-], and a
// sane length bound (defense-in-depth before the name reaches an exec argv).
func validInterceptMuxName(name string) bool {
	if !strings.HasPrefix(name, interceptMuxPrefix) {
		return false
	}
	if len(name) == len(interceptMuxPrefix) || len(name) > len(interceptMuxPrefix)+64 {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
		default:
			return false
		}
	}
	return true
}

// interceptSessionInfo is the secret-free metadata of one resume tmux session,
// read back from the session environment (set by interceptResumeSetMeta).
type interceptSessionInfo struct {
	Name      string
	Pod       string
	Namespace string
	Cluster   string
	Actor     string
	Mode      string // comma-separated modes, e.g. "egress,incoming"
	StartedAt time.Time
}

// view maps the tmux metadata into the same JSON shape the fallback registry
// emits, so /intercept/sessions returns one uniform list. The id is the tmux
// session name (how a Stop request routes back to kill-session).
func (si interceptSessionInfo) view() webInterceptView {
	return webInterceptView{
		ID:        si.Name,
		Cluster:   si.Cluster,
		Namespace: si.Namespace,
		Pod:       si.Pod,
		Actor:     si.Actor,
		Modes:     splitModes(si.Mode),
		StartedAt: si.StartedAt,
	}
}

// splitModes turns a "egress,incoming" CSV back into a slice, dropping blanks.
func splitModes(csv string) []string {
	var out []string
	for _, p := range strings.Split(csv, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseTmuxSessionNames returns the trimmed, non-empty lines of a
// `tmux list-sessions -F '#{session_name}'` output.
func parseTmuxSessionNames(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			names = append(names, line)
		}
	}
	return names
}

// parseTmuxEnvironment parses `tmux show-environment` output into a map. Lines
// are KEY=value; a leading '-' marks a removed var (skipped), and lines without
// '=' are skipped. The value keeps everything after the first '='.
func parseTmuxEnvironment(out string) map[string]string {
	env := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "-") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		env[k] = v
	}
	return env
}

// tmuxListHoneyIntercept lists the live resume sessions with their secret-free
// metadata. A tmux error (e.g. no server running, tmux absent) yields an empty
// list rather than an error — the caller unions it with the fallback registry.
func tmuxListHoneyIntercept() []interceptSessionInfo {
	out, err := tmuxRun("list-sessions", "-F", "#{session_name}")
	if err != nil {
		return nil
	}
	var infos []interceptSessionInfo
	for _, name := range parseTmuxSessionNames(string(out)) {
		if !validInterceptMuxName(name) {
			continue
		}
		info := interceptSessionInfo{Name: name}
		if envOut, err := tmuxRun("show-environment", "-t", name); err == nil {
			env := parseTmuxEnvironment(string(envOut))
			info.Pod = env["HONEY_INT_POD"]
			info.Namespace = env["HONEY_INT_NS"]
			info.Cluster = env["HONEY_INT_CLUSTER"]
			info.Actor = env["HONEY_INT_ACTOR"]
			info.Mode = env["HONEY_INT_MODE"]
			if ts := env["HONEY_INT_STARTED"]; ts != "" {
				if t, perr := time.Parse(time.RFC3339, ts); perr == nil {
					info.StartedAt = t
				}
			}
		}
		infos = append(infos, info)
	}
	return infos
}

// interceptResumeStop kills a resume session by name (guarded kill-session).
func interceptResumeStop(name string) error {
	if !validInterceptMuxName(name) {
		return fmt.Errorf("intercept: refusing to stop invalid session name %q", name)
	}
	out, err := tmuxRun("kill-session", "-t", name)
	if err == nil {
		return nil
	}
	// kill-session exits non-zero when the session is already gone — which is the
	// teardown goal (the pane exited on its own, or a prior stop already killed
	// it), not a failure. Only a session that still exists after a failed kill is
	// a real error worth surfacing.
	if !tmuxHasInterceptSession(name) {
		return nil
	}
	return fmt.Errorf("intercept: kill resume session %q: %w: %s", name, err, strings.TrimSpace(string(out)))
}

// tmuxHasInterceptSession reports whether a honey-int-* session is still live.
func tmuxHasInterceptSession(name string) bool {
	if !validInterceptMuxName(name) {
		return false
	}
	_, err := tmuxRun("has-session", "-t", name)
	return err == nil
}

// interceptResumeCloseTabKill returns the resume path's close_tab (×) teardown
// kill for ptyProxyTeardown. The honey_* killers it would otherwise reach gate
// on validHoneyMuxSessionName and are inert for honey-int-* names, so × would
// close the tab while the pane, its relay and the ephemeral container kept
// running.
func interceptResumeCloseTabKill(name string) func() {
	return func() {
		if err := interceptResumeStop(name); err != nil {
			zap.L().Warn("intercept resume: close_tab kill failed", zap.String("session", name), zap.Error(err))
		}
	}
}

// interceptSessionActor returns the HONEY_INT_ACTOR recorded for a
// honey-int-* resume session by interceptResumeSetMeta, or "" when name is
// invalid, tmux/the session cannot be reached, or no actor was ever recorded.
// Callers (MED-3: applyLiveTerminalShare's live-share ownership check) treat
// "" as "unknown" rather than "no owner" — they must not fail closed on it,
// since a transient tmux hiccup or an in-flight metadata write (see
// interceptResumeSetMeta's bounded retry) is common and must not block an
// otherwise legitimate request.
func interceptSessionActor(name string) string {
	if !validInterceptMuxName(name) {
		return ""
	}
	out, err := tmuxRun("show-environment", "-t", name)
	if err != nil {
		return ""
	}
	return parseTmuxEnvironment(string(out))["HONEY_INT_ACTOR"]
}

// interceptResumeSetMeta records the secret-free metadata (pod/ns/cluster/actor/
// modes + a start timestamp) into a resume session's tmux environment, so
// tmuxListHoneyIntercept can read it back. It is a no-op for an invalid name,
// and a no-op when the metadata is already there (an attach: rewriting it would
// reset HONEY_INT_STARTED) — but it DOES write on an attach to a session whose
// earlier write failed, so a session can never stay unlisted forever. tmux
// creates the session asynchronously after pty.Start, so the first write is
// retried on a bounded budget until the session exists.
// ponytail: fixed ~500ms poll budget; fine for a human-paced session start.
func interceptResumeSetMeta(name, pod, namespace, cluster, actor, modeCSV string) {
	if !validInterceptMuxName(name) {
		return
	}
	if out, err := tmuxRun("show-environment", "-t", name); err == nil {
		if parseTmuxEnvironment(string(out))["HONEY_INT_POD"] != "" {
			return
		}
	}
	rest := [][2]string{
		{"HONEY_INT_NS", namespace},
		{"HONEY_INT_CLUSTER", cluster},
		{"HONEY_INT_ACTOR", actor},
		{"HONEY_INT_MODE", modeCSV},
		{"HONEY_INT_STARTED", time.Now().UTC().Format(time.RFC3339)},
	}
	// "--" ends tmux's option parsing so a value with a leading '-' (actor is a
	// free-form SSO field) is never eaten as a flag.
	for i := 0; i < 20; i++ {
		if _, err := tmuxRun("set-environment", "-t", name, "--", "HONEY_INT_POD", pod); err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	for _, kv := range rest {
		_, _ = tmuxRun("set-environment", "-t", name, "--", kv[0], kv[1])
	}
}
