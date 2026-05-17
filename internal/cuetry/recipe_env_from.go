package cuetry

import (
	"context"
	"fmt"
	"strings"

	"github.com/shareed2k/honey/internal/hosts"
)

const envFromSourceStdout = "stdout"

const maxStepOutputBytes = 64 << 10

// EnvFromRef maps an environment variable from a dependency step's captured output.
type EnvFromRef struct {
	Step string            `json:"step"`
	Map  map[string]string `json:"map"`
}

func validateStepEnvFrom(stepIdx int, kind StepKind, mode ExecutionMode, step RecipeStep) error {
	if len(step.EnvFrom) == 0 {
		return nil
	}
	if mode == ExecutionModeLinear {
		return fmt.Errorf("cuetry: steps[%d].env_from is only allowed when recipe.type is \"graph\"", stepIdx)
	}
	if kind != StepKindCommand && kind != StepKindScript && kind != StepKindPlugin {
		return fmt.Errorf("cuetry: steps[%d]: env_from is only supported for command, script, and plugin steps", stepIdx)
	}
	return nil
}

func validateEnvFromRefs(stepIdx int, step RecipeStep, sg *StepGraph) error {
	if len(step.EnvFrom) == 0 {
		return nil
	}
	depSet := make(map[string]struct{}, len(step.Depends))
	for _, d := range step.Depends {
		depSet[strings.TrimSpace(d)] = struct{}{}
	}
	for i, ref := range step.EnvFrom {
		refStep := strings.TrimSpace(ref.Step)
		if refStep == "" {
			return fmt.Errorf("cuetry: steps[%d].env_from[%d].step is required", stepIdx, i)
		}
		if _, ok := sg.IDToIndex[refStep]; !ok {
			return fmt.Errorf("cuetry: steps[%d].env_from[%d] references unknown step id %q", stepIdx, i, refStep)
		}
		if _, ok := depSet[refStep]; !ok {
			return fmt.Errorf("cuetry: steps[%d].env_from[%d].step %q must appear in depends", stepIdx, i, refStep)
		}
		if len(ref.Map) == 0 {
			return fmt.Errorf("cuetry: steps[%d].env_from[%d].map is required", stepIdx, i)
		}
		for envKey, src := range ref.Map {
			if err := validateOneEnv(envKey, "x"); err != nil {
				return fmt.Errorf("cuetry: steps[%d].env_from[%d].map key: %w", stepIdx, i, err)
			}
			if strings.TrimSpace(src) != envFromSourceStdout {
				return fmt.Errorf("cuetry: steps[%d].env_from[%d].map[%q] must be %q", stepIdx, i, envKey, envFromSourceStdout)
			}
		}
	}
	return nil
}

// MergeEnvFromInto resolves env_from into dst (execute mode). Fails if a mapped value is missing.
func MergeEnvFromInto(dst map[string]string, step RecipeStep, store *StepOutputStore, hostName string, dryRun bool) error {
	if len(step.EnvFrom) == 0 {
		return nil
	}
	for i, ref := range step.EnvFrom {
		refStep := strings.TrimSpace(ref.Step)
		for envKey, src := range ref.Map {
			if strings.TrimSpace(src) != envFromSourceStdout {
				continue
			}
			if dryRun {
				dst[envKey] = fmt.Sprintf("<<stdout from step %q>>", refStep)
				continue
			}
			if store == nil {
				return fmt.Errorf("env_from: no output store for step %q", refStep)
			}
			val, ok := store.Get(refStep, hostName)
			if !ok {
				return fmt.Errorf("env_from: step %q has no stdout for host %q", refStep, hostName)
			}
			if err := validateOneEnv(envKey, val); err != nil {
				return fmt.Errorf("env_from step %q key %q: %w", refStep, envKey, err)
			}
			dst[envKey] = val
		}
		_ = i
	}
	return nil
}

// RecipeNeedsStepOutputCapture reports whether any step may need stdout capture.
func RecipeNeedsStepOutputCapture(r Recipe) bool {
	mode, err := RecipeExecutionMode(r)
	if err != nil || mode != ExecutionModeGraph {
		return false
	}
	if len(StepIDsReferencedByEnvFrom(r)) > 0 || len(StepIDsReferencedByWhen(r)) > 0 {
		return true
	}
	for _, s := range r.Steps {
		if len(s.EnvFrom) > 0 || strings.TrimSpace(s.When) != "" {
			return true
		}
	}
	return false
}

// StepIDsReferencedByEnvFrom returns step ids that should capture stdout (sources in env_from).
func StepIDsReferencedByEnvFrom(r Recipe) map[string]struct{} {
	out := make(map[string]struct{})
	for _, s := range r.Steps {
		for _, ref := range s.EnvFrom {
			if id := strings.TrimSpace(ref.Step); id != "" {
				out[id] = struct{}{}
			}
		}
	}
	for id := range StepIDsReferencedByWhen(r) {
		out[id] = struct{}{}
	}
	return out
}

func validateHoneyStepIDForGraph(recipe *Recipe, step RecipeStep) error {
	if recipe == nil {
		return nil
	}
	mode, err := RecipeExecutionMode(*recipe)
	if err != nil {
		return err
	}
	if mode != ExecutionModeGraph {
		return nil
	}
	id := strings.TrimSpace(step.ID)
	if id == "" {
		return nil
	}
	return validateOneEnv("HONEY_STEP_ID", id)
}

func mergeHoneyStepID(dst map[string]string, recipe *Recipe, step RecipeStep) {
	if recipe == nil {
		return
	}
	mode, _ := RecipeExecutionMode(*recipe)
	if mode != ExecutionModeGraph {
		return
	}
	id := strings.TrimSpace(step.ID)
	if id == "" {
		return
	}
	dst["HONEY_STEP_ID"] = id
}

// EffectiveEnvForRunOpts carries optional recipe-level context for env merge.
type EffectiveEnvForRunOpts struct {
	Recipe      *Recipe
	OutputStore *StepOutputStore
	DryRun      bool
}

// EffectiveEnvForRunEx merges env like EffectiveEnvForRun with env_from and HONEY_STEP_ID.
func EffectiveEnvForRunEx(ctx context.Context, resolveSecrets bool, resolver SecretResolver, step RecipeStep, defaults *RecipeDefaults, cliEnv map[string]string, r *hosts.Record, opts *EffectiveEnvForRunOpts) (map[string]string, error) {
	merged, err := EffectiveEnvForRun(ctx, resolveSecrets, resolver, step, defaults, cliEnv, r)
	if err != nil {
		return nil, err
	}
	var recipe *Recipe
	var store *StepOutputStore
	dryRun := false
	if opts != nil {
		recipe = opts.Recipe
		store = opts.OutputStore
		dryRun = opts.DryRun
	}
	if recipe != nil {
		if err := validateHoneyStepIDForGraph(recipe, step); err != nil {
			return nil, err
		}
		mergeHoneyStepID(merged, recipe, step)
	}
	hostName := ""
	if r != nil {
		hostName = r.Name
	}
	if err := MergeEnvFromInto(merged, step, store, hostName, dryRun); err != nil {
		return nil, err
	}
	return merged, nil
}
