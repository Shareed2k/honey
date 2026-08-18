package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/intercept"
)

// interceptGoleakOpts ignores GlobalTunnelPool's process-wide background
// sweep goroutine, started once (and never stopped) the moment any test in
// this package builds a tunnel coordinator — a pre-existing condition
// unrelated to the intercept code these tests exercise. Mirrors the same
// exclusion used for this exact leak elsewhere in the repo (e.g.
// internal/sshgateway, internal/provider/honeyprovider).
func interceptGoleakOpts() []goleak.Option {
	return []goleak.Option{goleak.IgnoreTopFunction("github.com/shareed2k/honey/internal/engine.(*GlobalTunnelPool).sweepLoop")}
}

// fakeCloseLog is a concurrency-safe ordered record of the names passed to
// fakeInterceptLive.Close, so a coordinator test can assert teardown order
// across several fakes without depending on map iteration order.
type fakeCloseLog struct {
	mu    sync.Mutex
	order []string
}

func (l *fakeCloseLog) record(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.order = append(l.order, name)
}

func (l *fakeCloseLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.order...)
}

// fakeInterceptLive is the interceptLive test double used in place of a real
// *intercept.Live. Run does not exercise a real data-plane session: it type
// asserts the passed runner back to captureRunner (the only production
// intercept.LocalRunner the executor ever builds) and writes canned output
// straight into its buffer, so a test controls res.Output without a real
// injected process. Close records every reason it was called with (for
// close-exactly-once assertions) and, when name/log are set, appends name to
// the shared log (for close-order assertions).
type fakeInterceptLive struct {
	name string
	log  *fakeCloseLog

	mu       sync.Mutex
	output   string
	runErr   error
	runCalls int
	commands [][]string
	closes   []string
}

func (f *fakeInterceptLive) Run(_ context.Context, runner intercept.LocalRunner, command []string) error {
	f.mu.Lock()
	f.runCalls++
	f.commands = append(f.commands, command)
	f.mu.Unlock()
	if cr, ok := runner.(captureRunner); ok && f.output != "" {
		cr.buf.WriteString(f.output)
	}
	return f.runErr
}

func (f *fakeInterceptLive) Close(reason string) {
	f.mu.Lock()
	f.closes = append(f.closes, reason)
	f.mu.Unlock()
	if f.log != nil {
		f.log.record(f.name)
	}
}

func (f *fakeInterceptLive) closeReasons() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.closes...)
}

func (f *fakeInterceptLive) runCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runCalls
}

func (f *fakeInterceptLive) lastCommand() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.commands) == 0 {
		return nil
	}
	return f.commands[len(f.commands)-1]
}

// stubEstablishInterceptSession swaps the establishInterceptSession seam for
// the duration of a test and restores it on cleanup, so the executor never
// reaches for a real cluster.
func stubEstablishInterceptSession(t *testing.T, fn func(ctx context.Context, cfg *config.File, opts intercept.Options) (interceptLive, error)) {
	t.Helper()
	orig := establishInterceptSession
	establishInterceptSession = fn
	t.Cleanup(func() { establishInterceptSession = orig })
}

// writeInterceptConfig writes a minimal honey config with intercept enabled
// and the given max_sessions cap, returning its path.
func writeInterceptConfig(t *testing.T, maxSessions int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := fmt.Sprintf("version: 1\nintercept:\n  enabled: true\n  agent_image: test/agent:latest\n  max_sessions: %d\n", maxSessions)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func newInterceptStep(id string, ic *cuetry.RecipeStepIntercept) *cuetry.InterceptStep {
	return &cuetry.InterceptStep{StepBase: cuetry.StepBase{ID: id, Host: "*"}, Intercept: ic}
}

func runInterceptExecuteStream(t *testing.T, req ExecutionRequest, opts ExecutionOptions) HostExecResult {
	t.Helper()
	opts.Execute = true
	ch := make(chan HostExecResult, 1)
	e := &InterceptExecutor{}
	require.NoError(t, e.ExecuteStream(context.Background(), req, opts, ch))
	select {
	case res := <-ch:
		return res
	default:
		t.Fatal("ExecuteStream produced no result")
		return HostExecResult{}
	}
}

// TestInterceptExecutor_establish proves the establishing-step path: the
// establishInterceptSession seam is called exactly once with the intercept
// Options built from the step and config, the resulting session is
// registered under the step id, the step's command runs through it, and the
// captured output/success flow into the result.
func TestInterceptExecutor_establish(t *testing.T) {
	defer goleak.VerifyNone(t, interceptGoleakOpts()...)

	cfgPath := writeInterceptConfig(t, 8)
	fake := &fakeInterceptLive{output: "hello from agent\n"}

	var establishCalls int
	var gotOpts intercept.Options
	stubEstablishInterceptSession(t, func(_ context.Context, cfg *config.File, opts intercept.Options) (interceptLive, error) {
		establishCalls++
		gotOpts = opts
		require.NotNil(t, cfg)
		return fake, nil
	})

	coord := NewRecipeInterceptCoordinator()
	defer coord.Close()

	step := newInterceptStep("est1", &cuetry.RecipeStepIntercept{
		Mode:       []string{"egress"},
		Targetless: true,
		Cluster:    "prod",
		Namespace:  "apps",
		Command:    "echo hi",
	})
	req := ExecutionRequest{Index: 0, Step: step}
	opts := ExecutionOptions{ConfigPath: cfgPath, InterceptCoord: coord}

	res := runInterceptExecuteStream(t, req, opts)

	assert.Equal(t, 1, establishCalls)
	assert.True(t, gotOpts.Targetless)
	assert.Equal(t, "prod", gotOpts.Cluster)
	assert.Equal(t, "apps", gotOpts.Namespace)
	assert.Equal(t, "test/agent:latest", gotOpts.AgentImage)

	assert.True(t, res.Success)
	assert.Equal(t, "hello from agent\n", res.Output)
	assert.Equal(t, 1, fake.runCount())
	assert.Equal(t, []string{"/bin/sh", "-c", "echo hi"}, fake.lastCommand())

	live, ok := coord.Lookup("est1")
	require.True(t, ok)
	assert.Same(t, fake, live)
	// The establishing step's own command must not close the session: the
	// coordinator (deferred above) is the only thing that tears it down.
	assert.Empty(t, fake.closeReasons())
}

// TestInterceptExecutor_defaultModeEgress proves an establishing step with no
// mode set at all runs with egress by default (matching the CLI's targetless
// default in resolveDirectModes), instead of failing at execute time with a
// ParseModes "at least one mode is required" error — Validate allows an
// empty mode list precisely because the executor is expected to default it.
func TestInterceptExecutor_defaultModeEgress(t *testing.T) {
	defer goleak.VerifyNone(t, interceptGoleakOpts()...)

	var gotOpts intercept.Options
	stubEstablishInterceptSession(t, func(_ context.Context, _ *config.File, opts intercept.Options) (interceptLive, error) {
		gotOpts = opts
		return &fakeInterceptLive{}, nil
	})

	coord := NewRecipeInterceptCoordinator()
	defer coord.Close()

	step := newInterceptStep("est-nomode", &cuetry.RecipeStepIntercept{
		Targetless: true, Cluster: "prod", Namespace: "apps", Command: "echo hi",
	})
	opts := ExecutionOptions{ConfigPath: writeInterceptConfig(t, 8), InterceptCoord: coord}
	res := runInterceptExecuteStream(t, ExecutionRequest{Index: 0, Step: step}, opts)

	require.True(t, res.Success, "an omitted mode must default to egress, not fail at execute: %s", res.ErrMsg)
	assert.True(t, gotOpts.Modes.Egress)
}

// TestInterceptExecutor_registerCapRejectedClosesRace proves the atomic cap
// check inside Register — not just the pre-Establish Count() fast path —
// rejects a session that only goes over cap during Establish (e.g. a
// same-wave sibling establishing step winning the race and registering its
// own session first), and that the executor closes the just-established but
// rejected live and fails the step, rather than leaking a deployed agent
// nobody tracks.
func TestInterceptExecutor_registerCapRejectedClosesRace(t *testing.T) {
	defer goleak.VerifyNone(t, interceptGoleakOpts()...)

	coord := NewRecipeInterceptCoordinator()
	defer coord.Close()

	fake := &fakeInterceptLive{}
	stubEstablishInterceptSession(t, func(context.Context, *config.File, intercept.Options) (interceptLive, error) {
		// Simulate a same-wave sibling step winning the race and registering
		// its own session between this step's Count() fast-path check and
		// this step's own Register call below.
		require.NoError(t, coord.Register("sibling", &fakeInterceptLive{}, 0))
		return fake, nil
	})

	step := newInterceptStep("est-race", &cuetry.RecipeStepIntercept{
		Mode: []string{"egress"}, Targetless: true, Cluster: "prod", Namespace: "apps",
		Command: "echo hi",
	})
	opts := ExecutionOptions{ConfigPath: writeInterceptConfig(t, 1), InterceptCoord: coord}
	res := runInterceptExecuteStream(t, ExecutionRequest{Index: 0, Step: step}, opts)

	assert.False(t, res.Success)
	assert.Contains(t, res.ErrMsg, "max_sessions")
	assert.Equal(t, []string{"max_sessions"}, fake.closeReasons(), "the rejected live must be closed by the executor")

	_, ok := coord.Lookup("est-race")
	assert.False(t, ok, "a rejected session must not be registered")
	assert.Equal(t, 1, coord.Count(), "only the sibling's session should remain registered")
}

// TestInterceptExecutor_exitCode proves a runner error that errors.As unwraps
// to *exec.ExitError surfaces as a numeric res.ExitCode (never a
// string-parsed one), which is what failed_when/changed_when read.
func TestInterceptExecutor_exitCode(t *testing.T) {
	defer goleak.VerifyNone(t, interceptGoleakOpts()...)

	cmd := exec.Command("/bin/sh", "-c", "exit 7")
	runErr := cmd.Run()
	var ee *exec.ExitError
	require.ErrorAs(t, runErr, &ee)

	fake := &fakeInterceptLive{runErr: runErr}
	stubEstablishInterceptSession(t, func(context.Context, *config.File, intercept.Options) (interceptLive, error) {
		return fake, nil
	})

	coord := NewRecipeInterceptCoordinator()
	defer coord.Close()

	step := newInterceptStep("est-exit", &cuetry.RecipeStepIntercept{
		Mode: []string{"egress"}, Targetless: true, Cluster: "prod", Namespace: "apps",
		Command: "exit 7",
	})
	req := ExecutionRequest{Index: 0, Step: step}
	opts := ExecutionOptions{ConfigPath: writeInterceptConfig(t, 8), InterceptCoord: coord}

	res := runInterceptExecuteStream(t, req, opts)

	assert.False(t, res.Success)
	assert.Equal(t, 7, res.ExitCode)
	assert.Equal(t, runErr.Error(), res.ErrMsg)
}

// TestInterceptExecutor_sessionStepReuse proves a reuse step never calls the
// establish seam, instead looking the session up by session_step and running
// its command through the same live — and that an unknown session_step is a
// step-level failure, not a panic.
func TestInterceptExecutor_sessionStepReuse(t *testing.T) {
	defer goleak.VerifyNone(t, interceptGoleakOpts()...)

	fake := &fakeInterceptLive{output: "reused"}
	coord := NewRecipeInterceptCoordinator()
	defer coord.Close()
	require.NoError(t, coord.Register("est1", fake, 0))

	var establishCalls int
	stubEstablishInterceptSession(t, func(context.Context, *config.File, intercept.Options) (interceptLive, error) {
		establishCalls++
		return nil, errors.New("must not be called on reuse")
	})

	opts := ExecutionOptions{InterceptCoord: coord}

	reuseStep := newInterceptStep("reuse1", &cuetry.RecipeStepIntercept{SessionStep: "est1", Command: "echo two"})
	res := runInterceptExecuteStream(t, ExecutionRequest{Index: 1, Step: reuseStep}, opts)
	assert.Equal(t, 0, establishCalls)
	assert.True(t, res.Success)
	assert.Equal(t, "reused", res.Output)
	assert.Equal(t, 1, fake.runCount())
	assert.Equal(t, []string{"/bin/sh", "-c", "echo two"}, fake.lastCommand())

	missingStep := newInterceptStep("reuse2", &cuetry.RecipeStepIntercept{SessionStep: "nope", Command: "echo x"})
	res2 := runInterceptExecuteStream(t, ExecutionRequest{Index: 2, Step: missingStep}, opts)
	assert.Equal(t, 0, establishCalls)
	assert.False(t, res2.Success)
	assert.Contains(t, res2.ErrMsg, `"nope"`)
	assert.Contains(t, res2.ErrMsg, "no live session")
}

// TestInterceptExecutor_outputCapture proves ic.Output routes the captured
// combined output into opts.OutputCapture under that name.
func TestInterceptExecutor_outputCapture(t *testing.T) {
	defer goleak.VerifyNone(t, interceptGoleakOpts()...)

	fake := &fakeInterceptLive{output: "captured-bytes"}
	stubEstablishInterceptSession(t, func(context.Context, *config.File, intercept.Options) (interceptLive, error) {
		return fake, nil
	})

	coord := NewRecipeInterceptCoordinator()
	defer coord.Close()
	capture := cuetry.NewRecipeOutputCapture()

	step := newInterceptStep("cap1", &cuetry.RecipeStepIntercept{
		Mode: []string{"egress"}, Targetless: true, Cluster: "prod", Namespace: "apps",
		Command: "echo hi", Output: "captured_var",
	})
	opts := ExecutionOptions{ConfigPath: writeInterceptConfig(t, 8), InterceptCoord: coord, OutputCapture: capture}
	res := runInterceptExecuteStream(t, ExecutionRequest{Index: 0, Step: step}, opts)

	require.True(t, res.Success)
	got, ok := capture.Get("captured_var")
	require.True(t, ok)
	assert.Equal(t, "captured-bytes", got)
}

// TestInterceptExecutor_stepFailures is a table of establishing-step
// configuration/gate errors that must fail the step (res.Success == false,
// a clear res.ErrMsg) without ever reaching the establish seam.
func TestInterceptExecutor_stepFailures(t *testing.T) {
	defer goleak.VerifyNone(t, interceptGoleakOpts()...)

	cases := []struct {
		name        string
		configPath  func(t *testing.T) string
		ic          *cuetry.RecipeStepIntercept
		preRegister int
		wantErrSub  string
	}{
		{
			name:       "intercept disabled",
			configPath: func(t *testing.T) string { return writeConfigBody(t, "version: 1\n") },
			ic:         &cuetry.RecipeStepIntercept{Mode: []string{"egress"}, Targetless: true, Cluster: "c", Namespace: "ns", Command: "echo hi"},
			wantErrSub: "intercept is not enabled",
		},
		{
			name:       "missing agent image",
			configPath: func(t *testing.T) string { return writeConfigBody(t, "version: 1\nintercept:\n  enabled: true\n") },
			ic:         &cuetry.RecipeStepIntercept{Mode: []string{"egress"}, Targetless: true, Cluster: "c", Namespace: "ns", Command: "echo hi"},
			wantErrSub: "no agent image",
		},
		{
			name:        "max sessions reached",
			configPath:  func(t *testing.T) string { return writeInterceptConfig(t, 1) },
			ic:          &cuetry.RecipeStepIntercept{Mode: []string{"egress"}, Targetless: true, Cluster: "c", Namespace: "ns", Command: "echo hi"},
			preRegister: 1,
			wantErrSub:  "max_sessions",
		},
		{
			name:       "invalid mode",
			configPath: func(t *testing.T) string { return writeInterceptConfig(t, 8) },
			ic:         &cuetry.RecipeStepIntercept{Mode: []string{"bogus"}, Targetless: true, Cluster: "c", Namespace: "ns", Command: "echo hi"},
			wantErrSub: "unknown mode",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubEstablishInterceptSession(t, func(context.Context, *config.File, intercept.Options) (interceptLive, error) {
				t.Fatal("establishInterceptSession must not be called")
				return nil, nil
			})

			coord := NewRecipeInterceptCoordinator()
			defer coord.Close()
			for i := 0; i < tc.preRegister; i++ {
				require.NoError(t, coord.Register(fmt.Sprintf("pre-%d", i), &fakeInterceptLive{}, 0))
			}

			step := newInterceptStep("s", tc.ic)
			opts := ExecutionOptions{ConfigPath: tc.configPath(t), InterceptCoord: coord}
			res := runInterceptExecuteStream(t, ExecutionRequest{Index: 0, Step: step}, opts)

			assert.False(t, res.Success)
			assert.Contains(t, res.ErrMsg, tc.wantErrSub)
		})
	}
}

func writeConfigBody(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// TestInterceptExecutor_dryRun is a light smoke test for the dry-run plan
// line: it must never call the establish seam and must not panic on either
// an establishing or a session_step-reuse step.
func TestInterceptExecutor_dryRun(t *testing.T) {
	stubEstablishInterceptSession(t, func(context.Context, *config.File, intercept.Options) (interceptLive, error) {
		t.Fatal("dry run must never establish a session")
		return nil, nil
	})

	e := &InterceptExecutor{}
	establishing := newInterceptStep("s1", &cuetry.RecipeStepIntercept{
		Mode: []string{"egress"}, Targetless: true, Cluster: "prod", Namespace: "apps", Command: "echo hi",
	})
	out := &countingWriter{}
	require.NoError(t, e.ExecuteDryRun(context.Background(), ExecutionRequest{Index: 0, Step: establishing}, ExecutionOptions{Execute: false}, out))
	assert.Greater(t, out.n, 0)

	reuse := newInterceptStep("s2", &cuetry.RecipeStepIntercept{SessionStep: "s1", Command: "echo two"})
	out2 := &countingWriter{}
	require.NoError(t, e.ExecuteDryRun(context.Background(), ExecutionRequest{Index: 1, Step: reuse}, ExecutionOptions{Execute: false}, out2))
	assert.Greater(t, out2.n, 0)
}

// countingWriter counts bytes written, avoiding an unused-import dance with
// bytes.Buffer for a test that only cares that something was printed.
type countingWriter struct{ n int }

func (w *countingWriter) Write(p []byte) (int, error) {
	w.n += len(p)
	return len(p), nil
}
