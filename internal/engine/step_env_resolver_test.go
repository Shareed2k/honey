package engine

import (
	"context"
	"errors"
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
)

// fakeEnvResolver is a StepEnvResolver test double that returns canned env
// or a fixed error. It records each Resolve call for assertion.
type fakeEnvResolver struct {
	env   map[string]string
	err   error
	calls []resolveCall
}

type resolveCall struct {
	target         string
	resolveSecrets bool
	dryRun         bool
}

func (f *fakeEnvResolver) Resolve(_ context.Context, _ *cuetry.StepBase, target *hosts.Record, resolveSecrets, dryRun bool) (map[string]string, error) {
	f.calls = append(f.calls, resolveCall{target: target.Name, resolveSecrets: resolveSecrets, dryRun: dryRun})
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[string]string, len(f.env))
	maps.Copy(out, f.env)
	return out, nil
}

// Verify fakeEnvResolver satisfies the interface at compile time.
var _ StepEnvResolver = (*fakeEnvResolver)(nil)

func TestRunEnvResolver_Resolve_delegatesToStepEnv(t *testing.T) {
	t.Parallel()

	// Minimal CueRun with empty params — EffectiveEnvForRunEx returns an
	// empty map when there are no defaults, step env, secrets, or CLI env.
	run := &CueRun{
		Params: CueRecipeRunParams{
			CLIEnv: map[string]string{"CLI_KEY": "cli_val"},
		},
	}
	resolver := &runEnvResolver{run: run}

	step := &cuetry.StepBase{
		Env: map[string]string{"STEP_KEY": "step_val"},
	}
	target := &hosts.Record{Name: "host-a", PrimaryIP: "10.0.0.1"}

	env, err := resolver.Resolve(context.Background(), step, target, false, true)
	require.NoError(t, err)
	assert.Equal(t, "step_val", env["STEP_KEY"])
	assert.Equal(t, "cli_val", env["CLI_KEY"])
}

func TestRunEnvResolver_Resolve_propagatesError(t *testing.T) {
	t.Parallel()

	// A CueRun whose secret resolver always fails should surface the error.
	wantErr := errors.New("secret resolver exploded")
	run := &CueRun{
		Params: CueRecipeRunParams{
			SecretResolver: &failResolver{err: wantErr},
		},
	}
	resolver := &runEnvResolver{run: run}

	step := &cuetry.StepBase{
		Secrets: map[string]cuetry.RecipeSecret{"MY_SECRET": {Ref: "vault://my/path"}},
	}
	target := &hosts.Record{Name: "host-b"}

	_, err := resolver.Resolve(context.Background(), step, target, true, false)
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
}

func TestFakeEnvResolver_recordsCalls(t *testing.T) {
	t.Parallel()

	fake := &fakeEnvResolver{env: map[string]string{"FOO": "bar"}}
	target := &hosts.Record{Name: "web-01"}

	env, err := fake.Resolve(context.Background(), &cuetry.StepBase{}, target, true, false)
	require.NoError(t, err)
	assert.Equal(t, "bar", env["FOO"])
	require.Len(t, fake.calls, 1)
	assert.Equal(t, "web-01", fake.calls[0].target)
	assert.True(t, fake.calls[0].resolveSecrets)
	assert.False(t, fake.calls[0].dryRun)
}

func TestFakeEnvResolver_propagatesError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("injected env error")
	fake := &fakeEnvResolver{err: wantErr}
	target := &hosts.Record{Name: "db-01"}

	_, err := fake.Resolve(context.Background(), &cuetry.StepBase{}, target, false, true)
	assert.ErrorIs(t, err, wantErr)
}

// failResolver is a cuetry.SecretResolver that always returns an error.
type failResolver struct{ err error }

func (f *failResolver) Handles(_ string) bool                               { return true }
func (f *failResolver) Resolve(_ context.Context, _ string) (string, error) { return "", f.err }
