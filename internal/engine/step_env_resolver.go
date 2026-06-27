package engine

import (
	"context"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
)

// StepEnvResolver resolves the effective env map for one step on one target.
// It is the single interface behind which all env wiring (defaults, CLI env,
// secrets, OutputStore, KV, env_from) is hidden. Executors call Resolve; they
// are unaware of CueRun internals.
//
// Callers that need an error returned per-target should check the error
// inline; callers that embed the call in a closure (e.g. cmdFunc) should
// format the error into the remote command string as a fallback.
type StepEnvResolver interface {
	Resolve(ctx context.Context, step *cuetry.StepBase, target *hosts.Record, resolveSecrets, dryRun bool) (map[string]string, error)
}

// runEnvResolver delegates to CueRun.StepEnv, the single place that assembles
// all run-scoped inputs: secret resolver, recipe defaults, CLI env, prior-step
// OutputStore, OutputCapture, and live KV.
type runEnvResolver struct {
	run *CueRun
}

func (r *runEnvResolver) Resolve(ctx context.Context, step *cuetry.StepBase, target *hosts.Record, resolveSecrets, dryRun bool) (map[string]string, error) {
	return r.run.StepEnv(ctx, step, target, resolveSecrets, dryRun)
}
