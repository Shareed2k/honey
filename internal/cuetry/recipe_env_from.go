package cuetry

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shareed2k/honey/internal/hosts"
)

const envFromSourceStdout = "stdout"

const maxStepOutputBytes = 64 << 10

// EnvFromRef maps an environment variable from a dependency step's captured output, jq extract, or KV.
type EnvFromRef struct {
	Step       string            `json:"step,omitempty"`
	FromOutput string            `json:"from_output,omitempty"`
	Map        map[string]string `json:"map,omitempty"`
	Extract    map[string]string `json:"extract,omitempty"`
	Kv         map[string]string `json:"kv,omitempty"`
}

func validateStepEnvFrom(stepIdx int, kind string, mode ExecutionMode, step *StepBase) error {
	if len(step.EnvFrom) == 0 {
		return nil
	}
	if mode == ExecutionModeLinear {
		return fmt.Errorf("cuetry: steps[%d].env_from is only allowed when recipe.type is \"graph\"", stepIdx)
	}
	if kind != KindCommand && kind != KindScript && kind != KindPlugin && kind != KindTemplate && kind != KindK8s {
		return fmt.Errorf("cuetry: steps[%d]: env_from is only supported for command, script, plugin, template, and k8s steps", stepIdx)
	}
	return nil
}

func validateEnvFromRefs(stepIdx int, step *StepBase, sg *StepGraph, outputByName map[string]string) error {
	if len(step.EnvFrom) == 0 {
		return nil
	}
	depSet := make(map[string]struct{}, len(step.Depends))
	for _, d := range step.Depends {
		depSet[strings.TrimSpace(d)] = struct{}{}
	}
	seenEnv := make(map[string]int)
	for i, ref := range step.EnvFrom {
		hasMap := len(ref.Map) > 0
		hasExtract := len(ref.Extract) > 0
		hasKv := len(ref.Kv) > 0
		if !hasMap && !hasExtract && !hasKv {
			return fmt.Errorf("cuetry: steps[%d].env_from[%d]: at least one of map, extract, or kv is required", stepIdx, i)
		}
		refStep := strings.TrimSpace(ref.Step)
		refOut := strings.TrimSpace(ref.FromOutput)
		hasStep := refStep != ""
		hasOut := refOut != ""
		kvOnly := hasKv && !hasMap && !hasExtract
		if kvOnly {
			if hasStep || hasOut {
				return fmt.Errorf("cuetry: steps[%d].env_from[%d]: kv-only entry must not set step or from_output", stepIdx, i)
			}
		} else if hasMap || hasExtract {
			if hasStep == hasOut {
				return fmt.Errorf("cuetry: steps[%d].env_from[%d]: exactly one of step or from_output is required", stepIdx, i)
			}
			if hasStep {
				if _, ok := sg.IDToIndex[refStep]; !ok {
					return fmt.Errorf("cuetry: steps[%d].env_from[%d] references unknown step id %q", stepIdx, i, refStep)
				}
				if _, ok := depSet[refStep]; !ok {
					return fmt.Errorf("cuetry: steps[%d].env_from[%d].step %q must appear in depends", stepIdx, i, refStep)
				}
			}
			if hasOut {
				producer, ok := outputByName[refOut]
				if !ok {
					return fmt.Errorf("cuetry: steps[%d].env_from[%d].from_output references unknown capture name %q", stepIdx, i, refOut)
				}
				if _, ok := depSet[producer]; !ok {
					return fmt.Errorf("cuetry: steps[%d].env_from[%d].from_output %q: producer step %q must appear in depends", stepIdx, i, refOut, producer)
				}
			}
		}
		validateKeys := func(kind string, m map[string]string, fn func(string) error) error {
			for envKey, val := range m {
				if prev, dup := seenEnv[envKey]; dup {
					return fmt.Errorf("cuetry: steps[%d].env_from[%d]: duplicate env key %q (also in env_from[%d])", stepIdx, i, envKey, prev)
				}
				seenEnv[envKey] = i
				if err := validateOneEnv(envKey, "x"); err != nil {
					return fmt.Errorf("cuetry: steps[%d].env_from[%d].%s key: %w", stepIdx, i, kind, err)
				}
				if err := fn(val); err != nil {
					return fmt.Errorf("cuetry: steps[%d].env_from[%d].%s[%q]: %w", stepIdx, i, kind, envKey, err)
				}
			}
			return nil
		}
		if err := validateKeys("map", ref.Map, func(src string) error {
			if strings.TrimSpace(src) != envFromSourceStdout {
				return fmt.Errorf("must be %q", envFromSourceStdout)
			}
			return nil
		}); err != nil {
			return err
		}
		if err := validateKeys("extract", ref.Extract, ValidateJQQuery); err != nil {
			return err
		}
		if err := validateKeys("kv", ref.Kv, func(kvKey string) error {
			return stepkvValidateKey(strings.TrimSpace(kvKey))
		}); err != nil {
			return err
		}
	}
	return nil
}

// templateOutputProducers maps named capture names (template.output or k8s.output) to producer step ids.
func templateOutputProducers(steps []StepWrapper) map[string]string {
	out := make(map[string]string)
	for _, w := range steps {
		id := strings.TrimSpace(w.Step.Base().ID)
		if ts, ok := w.Step.(*TemplateStep); ok && ts.Template != nil {
			if name := strings.TrimSpace(ts.Template.Output); name != "" {
				out[name] = id
			}
		}
		if ks, ok := w.Step.(*K8sStep); ok && ks.K8s != nil {
			if name := strings.TrimSpace(ks.K8s.Output); name != "" {
				out[name] = id
			}
		}
	}
	return out
}

// validateUniqueTemplateOutputs ensures named capture names (template.output / k8s.output) are unique in a recipe.
func validateUniqueTemplateOutputs(steps []StepWrapper) error {
	seen := make(map[string]int)
	check := func(i int, name, field string) error {
		if name == "" {
			return nil
		}
		if prev, dup := seen[name]; dup {
			return fmt.Errorf("cuetry: steps[%d].%s %q duplicates steps[%d]", i, field, name, prev)
		}
		seen[name] = i
		return nil
	}
	for i, w := range steps {
		if ts, ok := w.Step.(*TemplateStep); ok && ts.Template != nil {
			if err := check(i, strings.TrimSpace(ts.Template.Output), "template.output"); err != nil {
				return err
			}
		}
		if ks, ok := w.Step.(*K8sStep); ok && ks.K8s != nil {
			if err := check(i, strings.TrimSpace(ks.K8s.Output), "k8s.output"); err != nil {
				return err
			}
		}
	}
	return nil
}

// MergeEnvFromInto resolves env_from into dst (execute mode). Fails if a mapped value is missing.
func MergeEnvFromInto(dst map[string]string, step *StepBase, store *StepOutputStore, capture *RecipeOutputCapture, kv KVReader, hostName string, dryRun bool, matrixExpansions map[string][]string) error {
	if len(step.EnvFrom) == 0 {
		return nil
	}
	for _, ref := range step.EnvFrom {
		refStep := strings.TrimSpace(ref.Step)
		refOut := strings.TrimSpace(ref.FromOutput)
		for envKey, src := range ref.Map {
			if strings.TrimSpace(src) != envFromSourceStdout {
				continue
			}
			if dryRun {
				if refStep != "" {
					dst[envKey] = fmt.Sprintf("<<stdout from step %q>>", refStep)
				} else {
					dst[envKey] = fmt.Sprintf("<<stdout from output %q>>", refOut)
				}
				continue
			}
			val, err := envFromStdout(store, capture, refStep, refOut, hostName, matrixExpansions)
			if err != nil {
				return err
			}
			if err := validateOneEnv(envKey, val); err != nil {
				return fmt.Errorf("env_from key %q: %w", envKey, err)
			}
			dst[envKey] = val
		}
		if len(ref.Extract) > 0 {
			var doc string
			var err error
			if dryRun {
				for envKey, q := range ref.Extract {
					dst[envKey] = fmt.Sprintf("<<jq %s>>", strings.TrimSpace(q))
				}
				continue
			}
			doc, err = envFromStdout(store, capture, refStep, refOut, hostName, matrixExpansions)
			if err != nil {
				return err
			}
			for envKey, q := range ref.Extract {
				val, err := EvalJQ(doc, q)
				if err != nil {
					return fmt.Errorf("env_from extract key %q: %w", envKey, err)
				}
				if err := validateOneEnv(envKey, val); err != nil {
					return fmt.Errorf("env_from extract key %q: %w", envKey, err)
				}
				dst[envKey] = val
			}
		}
		for envKey, kvKey := range ref.Kv {
			kvKey = strings.TrimSpace(kvKey)
			if dryRun {
				dst[envKey] = fmt.Sprintf("<<kv %s>>", kvKey)
				continue
			}
			if kv == nil {
				return fmt.Errorf("env_from kv: no KV reader for key %q", kvKey)
			}
			val, found, err := kv.Get(kvKey)
			if err != nil {
				return fmt.Errorf("env_from kv key %q: %w", kvKey, err)
			}
			if !found {
				return fmt.Errorf("env_from kv key %q is not set", kvKey)
			}
			if err := validateOneEnv(envKey, val); err != nil {
				return fmt.Errorf("env_from kv env key %q: %w", envKey, err)
			}
			dst[envKey] = val
		}
	}
	return nil
}

func envFromStdout(store *StepOutputStore, capture *RecipeOutputCapture, refStep, refOut, hostName string, matrixExpansions map[string][]string) (string, error) {
	if refStep != "" {
		if store == nil {
			return "", fmt.Errorf("env_from: no output store for step %q", refStep)
		}

		if expanded, ok := matrixExpansions[refStep]; ok {
			var results []string
			for _, expID := range expanded {
				var val string
				var found bool
				if hostName != "" && hostName != MatchLocalAIHost {
					val, found = store.Get(expID, hostName)
				}
				if !found {
					val, found = store.FirstStdout(expID)
				}
				if found {
					results = append(results, val)
				}
			}
			if len(results) == 0 {
				return "", fmt.Errorf("env_from: step %q (matrix) has no stdout for host %q", refStep, hostName)
			}
			if len(results) != len(expanded) {
				return "", fmt.Errorf("env_from: step %q (matrix) missing stdout for some expanded nodes", refStep)
			}
			jsonArr, err := json.Marshal(results)
			if err != nil {
				return "", fmt.Errorf("env_from: failed to marshal matrix outputs: %w", err)
			}
			return string(jsonArr), nil
		}

		var val string
		var ok bool
		if hostName != "" && hostName != MatchLocalAIHost {
			val, ok = store.Get(refStep, hostName)
		}
		if !ok {
			val, ok = store.FirstStdout(refStep)
		}
		if !ok {
			return "", fmt.Errorf("env_from: step %q has no stdout for host %q", refStep, hostName)
		}
		return val, nil
	}
	if capture == nil {
		return "", fmt.Errorf("env_from: no output capture for %q", refOut)
	}
	val, ok := capture.Get(refOut)
	if !ok {
		return "", fmt.Errorf("env_from: output capture %q is not set", refOut)
	}
	return val, nil
}

// MergeEnvFromIntoTemplateData overlays env_from-resolved keys onto template data (graph mode).
func MergeEnvFromIntoTemplateData(data map[string]any, step *StepBase, store *StepOutputStore, capture *RecipeOutputCapture, kv KVReader, hostName string, dryRun bool, matrixExpansions map[string][]string) error {
	env := make(map[string]string)
	if err := MergeEnvFromInto(env, step, store, capture, kv, hostName, dryRun, matrixExpansions); err != nil {
		return err
	}
	for k, v := range env {
		data[k] = v
	}
	return nil
}

// PrepareTemplateData merges env_from and expands ${VAR} in data values (not the Go template body).
func PrepareTemplateData(data map[string]any, step *StepBase, store *StepOutputStore, capture *RecipeOutputCapture, kv KVReader, hostName string, extraEnv map[string]string, dryRun bool, matrixExpansions map[string][]string) error {
	if err := MergeEnvFromIntoTemplateData(data, step, store, capture, kv, hostName, dryRun, matrixExpansions); err != nil {
		return err
	}
	vars := BuildRecipeVarMap(capture, extraEnv)
	for k, v := range data {
		if s, ok := v.(string); ok {
			vars[k] = s
		}
	}
	return ExpandRecipeVarsInData(data, vars, !dryRun)
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
	for _, w := range r.Steps {
		b := w.Step.Base()
		if len(b.EnvFrom) > 0 || strings.TrimSpace(b.When) != "" {
			return true
		}
		if ts, ok := w.Step.(*TemplateStep); ok && ts.Template != nil && strings.TrimSpace(ts.Template.Output) != "" {
			return true
		}
	}
	return false
}

// StepIDsReferencedByEnvFrom returns step ids that should capture stdout (sources in env_from).
func StepIDsReferencedByEnvFrom(r Recipe) map[string]struct{} {
	out := make(map[string]struct{})
	for _, w := range r.Steps {
		for _, ref := range w.Step.Base().EnvFrom {
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

// OutputNamesReferencedByEnvFrom returns template.output names referenced via from_output.
func OutputNamesReferencedByEnvFrom(r Recipe) map[string]struct{} {
	out := make(map[string]struct{})
	for _, w := range r.Steps {
		for _, ref := range w.Step.Base().EnvFrom {
			if name := strings.TrimSpace(ref.FromOutput); name != "" {
				out[name] = struct{}{}
			}
		}
	}
	return out
}

func validateHoneyStepIDForGraph(recipe *Recipe, step *StepBase) error {
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

func mergeHoneyStepID(dst map[string]string, recipe *Recipe, step *StepBase) {
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
	Recipe        *Recipe
	OutputStore   *StepOutputStore
	OutputCapture *RecipeOutputCapture
	KV            KVReader
	DryRun        bool
}

// EffectiveEnvForRunEx merges env like EffectiveEnvForRun with env_from and HONEY_STEP_ID.
func EffectiveEnvForRunEx(ctx context.Context, resolveSecrets bool, resolver SecretResolver, step *StepBase, defaults *RecipeDefaults, cliEnv map[string]string, r *hosts.Record, opts *EffectiveEnvForRunOpts) (map[string]string, error) {
	merged, err := EffectiveEnvForRun(ctx, resolveSecrets, resolver, step, defaults, cliEnv, r)
	if err != nil {
		return nil, err
	}
	var recipe *Recipe
	var store *StepOutputStore
	var capture *RecipeOutputCapture
	var kv KVReader
	var matrixExp map[string][]string
	dryRun := false
	if opts != nil {
		recipe = opts.Recipe
		store = opts.OutputStore
		capture = opts.OutputCapture
		kv = opts.KV
		dryRun = opts.DryRun
	}
	if recipe != nil {
		if err := validateHoneyStepIDForGraph(recipe, step); err != nil {
			return nil, err
		}
		mergeHoneyStepID(merged, recipe, step)
		matrixExp = recipe.MatrixExpansions
	}
	hostName := ""
	if r != nil {
		hostName = r.Name
	}
	if err := MergeEnvFromInto(merged, step, store, capture, kv, hostName, dryRun, matrixExp); err != nil {
		return nil, err
	}
	return merged, nil
}
