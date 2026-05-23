package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
)

type coordinatorKVReader struct {
	coord *RecipeKVCoordinator
}

func (r coordinatorKVReader) Get(key string) (string, bool, error) {
	if r.coord == nil {
		return "", false, nil
	}
	sess, err := r.coord.EnsureSession()
	if err != nil {
		return "", false, err
	}
	return sess.Get(key)
}

func whenSkippedResult(r hosts.Record) HostExecResult {
	return HostExecResult{
		Name:     r.Name,
		IP:       r.PrimaryIP,
		Provider: r.Provider,
		Success:  false,
		Skipped:  true,
		Output:   "(skipped: when)",
	}
}

func compileStepWhen(step cuetry.RecipeStep) (*cuetry.WhenProgram, error) {
	w := strings.TrimSpace(step.When)
	if w == "" {
		return nil, nil
	}
	return cuetry.CompileWhen(w)
}

func buildWhenEvalOpts(ctx context.Context, recipe cuetry.Recipe, step cuetry.RecipeStep, host hosts.Record, dest *hosts.Record, store *cuetry.StepResultStore, secretResolver cuetry.SecretResolver, kv cuetry.KVReader, execute bool) (cuetry.WhenEvalOpts, error) {
	secrets, err := cuetry.BuildSecretsMapForWhen(ctx, execute, secretResolver, step, recipe.Defaults)
	if err != nil {
		return cuetry.WhenEvalOpts{}, err
	}
	hostName := host.Name
	if strings.TrimSpace(hostName) == "" {
		hostName = "_"
	}
	var steps map[string]cuetry.StepView
	if store != nil {
		steps = store.StepsViewForHost(hostName)
	} else {
		steps = map[string]cuetry.StepView{}
	}
	return cuetry.WhenEvalOpts{
		RecipeName: recipe.Name,
		Execute:    execute,
		Host:       host,
		Dest:       dest,
		Steps:      steps,
		Secrets:    secrets,
		KV:         kv,
	}, nil
}

func evalStepWhen(ctx context.Context, prog *cuetry.WhenProgram, recipe cuetry.Recipe, step cuetry.RecipeStep, host hosts.Record, dest *hosts.Record, store *cuetry.StepResultStore, secretResolver cuetry.SecretResolver, kv cuetry.KVReader, execute bool) (bool, error) {
	if prog == nil {
		return true, nil
	}
	opts, err := buildWhenEvalOpts(ctx, recipe, step, host, dest, store, secretResolver, kv, execute)
	if err != nil {
		return false, err
	}
	return cuetry.EvalWhen(prog, opts)
}

func filterTargetsByWhen(
	ctx context.Context,
	recipe cuetry.Recipe,
	step cuetry.RecipeStep,
	targets []hosts.Record,
	store *cuetry.StepResultStore,
	secretResolver cuetry.SecretResolver,
	kv cuetry.KVReader,
	execute bool,
) ([]hosts.Record, []HostExecResult, error) {
	prog, err := compileStepWhen(step)
	if err != nil {
		return nil, nil, err
	}
	if prog == nil {
		return targets, nil, nil
	}
	var kept []hosts.Record
	var skipped []HostExecResult
	for _, t := range targets {
		ok, err := evalStepWhen(ctx, prog, recipe, step, t, nil, store, secretResolver, kv, execute)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			kept = append(kept, t)
		} else {
			skipped = append(skipped, whenSkippedResult(t))
		}
	}
	return kept, skipped, nil
}

func evalAgentTransferWhen(ctx context.Context, recipe cuetry.Recipe, step cuetry.RecipeStep, src, dst hosts.Record, store *cuetry.StepResultStore, secretResolver cuetry.SecretResolver, kv cuetry.KVReader, execute bool) (bool, error) {
	prog, err := compileStepWhen(step)
	if err != nil {
		return false, err
	}
	if prog == nil {
		return true, nil
	}
	return evalStepWhen(ctx, prog, recipe, step, src, &dst, store, secretResolver, kv, execute)
}

func evalAIStepWhen(ctx context.Context, recipe cuetry.Recipe, step cuetry.RecipeStep, store *cuetry.StepResultStore, secretResolver cuetry.SecretResolver, kv cuetry.KVReader, execute bool) (bool, error) {
	prog, err := compileStepWhen(step)
	if err != nil {
		return false, err
	}
	if prog == nil {
		return true, nil
	}
	host := hosts.Record{Name: cuetry.MatchLocalAIHost}
	opts, err := buildWhenEvalOpts(ctx, recipe, step, host, nil, store, secretResolver, kv, execute)
	if err != nil {
		return false, err
	}
	if store != nil {
		opts.Steps = store.StepsViewAggregated()
	}
	return cuetry.EvalWhen(prog, opts)
}

func recordStepHostResults(store *cuetry.StepResultStore, stepID string, rows []HostExecResult) {
	if store == nil {
		return
	}
	stepID = strings.TrimSpace(stepID)
	if stepID == "" {
		return
	}
	for _, row := range rows {
		name := strings.TrimSpace(row.Name)
		if i := strings.LastIndex(name, " | "); i >= 0 {
			name = strings.TrimSpace(name[i+3:])
		}
		if name == "" {
			continue
		}
		store.RecordHost(stepID, name, cuetry.HostStepResult{
			Succeeded: row.Success && !row.Skipped,
			Skipped:   row.Skipped,
			ExitCode:  row.ExitCode,
			Stdout:    row.Output,
		})
	}
}

func allHostsWhenSkipped(rows []HostExecResult) bool {
	if len(rows) == 0 {
		return false
	}
	for _, r := range rows {
		if !r.Skipped {
			return false
		}
	}
	return true
}

func ensureKVSessionForRecipe(_ cuetry.Recipe, recipeKV *RecipeKVCoordinator, execute bool) error {
	if recipeKV == nil || !execute {
		return nil
	}
	_, err := recipeKV.EnsureSession()
	return err
}

// noopKVReader is used when no coordinator is available.
type noopKVReader struct{}

func (noopKVReader) Get(string) (string, bool, error) {
	return "", false, nil
}

func kvReaderFromCoordinator(coord *RecipeKVCoordinator) cuetry.KVReader {
	if coord == nil {
		return noopKVReader{}
	}
	return coordinatorKVReader{coord: coord}
}

func writeWhenDryLines(out interface{ Write([]byte) (int, error) }, stepIdx int, step cuetry.RecipeStep, recipe cuetry.Recipe, targets []hosts.Record, store *cuetry.StepResultStore, execute bool) error {
	w := strings.TrimSpace(step.When)
	if w == "" {
		return nil
	}
	_, _ = fmt.Fprintf(out, "  step %d when: %q\n", stepIdx, w)
	prog, err := compileStepWhen(step)
	if err != nil {
		return err
	}
	kv := noopKVReader{}
	for _, t := range targets {
		ok, err := evalStepWhen(context.Background(), prog, recipe, step, t, nil, store, nil, kv, execute)
		if err != nil {
			return err
		}
		if !ok {
			_, _ = fmt.Fprintf(out, "  step %d: name=%q when=false → skipped\n", stepIdx, t.Name)
		}
	}
	return nil
}
