package engine

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"time"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/policy"
)

func init() {
	RegisterStepExecutor(cuetry.KindOPA, &OPAExecutor{})
}

// OPAExecutor evaluates an inline rego policy step. It runs locally and emits a
// single result; a deny fails the step so later steps can gate on it.
type OPAExecutor struct{}

// ExecuteDryRun writes the step plan line without compiling or evaluating.
func (e *OPAExecutor) ExecuteDryRun(sc *StepContext) error {
	policyRef := ""
	if step, ok := sc.Step.(*cuetry.OPAStep); ok && step.OPA != nil {
		policyRef = step.OPA.Policy
	}
	_, _ = fmt.Fprintf(sc.Out,
		"step %d: kind=opa target=local policy=%q (evaluates rego; fails the step on deny)\n",
		sc.Index, policyRef)
	WriteCueStepNotifyDryLine(sc.Out, sc.Step)
	return nil
}

// ExecuteStream compiles and evaluates the policy, emitting one result.
func (e *OPAExecutor) ExecuteStream(sc *StepContext) error {
	stepStart := time.Now()
	step, ok := sc.Step.(*cuetry.OPAStep)
	if !ok || step.OPA == nil {
		return fmt.Errorf("opa: internal step type %T", sc.Step)
	}

	res := e.evaluate(sc.Ctx, sc.Run, step)
	AnnotateCueStepResult(&res, sc.Index, sc.Step, cuetry.KindOPA)
	sc.ResultCh <- res
	ObserveRecipeStep(sc.Run.Params.Obs, cuetry.KindOPA, stepStart, []HostExecResult{res}, 1)

	if !res.Success {
		return fmt.Errorf("%s", res.ErrMsg)
	}
	return nil
}

// evaluate loads the policy file (relative to the recipe dir), builds the input
// document (actor + recipe + author-supplied keys), and returns a result.
func (e *OPAExecutor) evaluate(ctx context.Context, run *CueRun, step *cuetry.OPAStep) HostExecResult {
	fail := func(format string, args ...any) HostExecResult {
		return HostExecResult{Name: "opa", Provider: "local", IP: "-", Success: false, ErrMsg: fmt.Sprintf(format, args...)}
	}

	policyPath := step.OPA.Policy
	if !filepath.IsAbs(policyPath) {
		policyPath = filepath.Join(run.Params.RecipeDir, policyPath)
	}
	// #nosec G304 -- policy path is recipe-author controlled and resolved within
	// the recipe directory; recipes are already trusted code in this engine.
	src, err := os.ReadFile(policyPath)
	if err != nil {
		return fail("read policy %q: %v", step.OPA.Policy, err)
	}

	enf, err := policy.NewFromSource(ctx, step.OPA.Policy, string(src))
	if err != nil {
		return fail("compile policy: %v", err)
	}

	actor := run.Params.ActorID
	if actor == "" {
		actor = "api"
	}
	input := map[string]any{
		"actor":  actor,
		"recipe": run.Params.Recipe.Name,
	}
	maps.Copy(input, step.OPA.Input)

	d, err := enf.Evaluate(ctx, input)
	if err != nil {
		return fail("evaluate: %v", err)
	}
	if !d.Allow {
		reason := d.DenyReason
		if reason == "" {
			reason = "policy denied"
		}
		return fail("%s", reason)
	}
	return HostExecResult{Name: "opa", Provider: "local", IP: "-", Success: true, Output: "policy: allow"}
}
