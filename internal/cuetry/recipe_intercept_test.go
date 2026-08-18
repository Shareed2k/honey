package cuetry

import (
	"strings"
	"testing"
)

func TestClassifyInterceptStep(t *testing.T) {
	step := &InterceptStep{
		StepBase: StepBase{Host: "_"},
		Intercept: &RecipeStepIntercept{
			Mode:       []string{"egress"},
			Targetless: true,
			Cluster:    "staging",
			Namespace:  "checkout",
			Command:    "curl svc.checkout.svc:8080/health",
		},
	}
	if err := step.Validate(StepValidateCtx{}); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if step.Kind() != KindIntercept {
		t.Errorf("expected KindIntercept, got %v", step.Kind())
	}
}

func TestInterceptStepValidate_missingBlock(t *testing.T) {
	step := &InterceptStep{StepBase: StepBase{Host: "_"}}
	err := step.Validate(StepValidateCtx{})
	if err == nil || !strings.Contains(err.Error(), "intercept block") {
		t.Fatalf("expected missing-block error, got %v", err)
	}
}

func TestValidateInterceptStep_ok(t *testing.T) {
	cases := map[string]*RecipeStepIntercept{
		"targetless establisher with explicit egress mode": {
			Mode:       []string{"egress"},
			Targetless: true,
			Cluster:    "staging",
			Namespace:  "checkout",
			Command:    "curl svc.checkout.svc:8080/health",
		},
		"targetless establisher with omitted mode": {
			Targetless: true,
			Cluster:    "staging",
			Namespace:  "checkout",
			Command:    "curl svc.checkout.svc:8080/health",
		},
		"session_step consumer with just script": {
			SessionStep: "establish",
			Script:      "run-suite.sh",
		},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateInterceptStep(StepValidateCtx{}, cfg); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestValidateInterceptStep_errors(t *testing.T) {
	cases := map[string]struct {
		intercept *RecipeStepIntercept
		want      string
	}{
		"neither command nor script": {
			&RecipeStepIntercept{Mode: []string{"egress"}, Targetless: true, Cluster: "c", Namespace: "n"},
			"requires one of command or script",
		},
		"both command and script": {
			&RecipeStepIntercept{Command: "curl x", Script: "run.sh"},
			"mutually exclusive",
		},
		"mode incoming rejected (targetless is egress-only)": {
			&RecipeStepIntercept{Command: "curl x", Mode: []string{"incoming"}, Targetless: true, Cluster: "c", Namespace: "n"},
			"supports only egress",
		},
		"mode env rejected (needs a target pod)": {
			&RecipeStepIntercept{Command: "curl x", Mode: []string{"env"}, Targetless: true, Cluster: "c", Namespace: "n"},
			"supports only egress",
		},
		"mode files rejected (needs a target pod)": {
			&RecipeStepIntercept{Command: "curl x", Mode: []string{"files"}, Targetless: true, Cluster: "c", Namespace: "n"},
			"supports only egress",
		},
		"targetless false rejected": {
			&RecipeStepIntercept{Command: "curl x", Targetless: false, Cluster: "c", Namespace: "n"},
			"only targetless",
		},
		"targetless absent rejected": {
			&RecipeStepIntercept{Command: "curl x", Cluster: "c", Namespace: "n"},
			"only targetless",
		},
		"env_include on establishing step rejected": {
			&RecipeStepIntercept{Command: "curl x", Targetless: true, Cluster: "c", Namespace: "n", EnvInclude: []string{"A"}},
			"env_include/env_exclude require env mode",
		},
		"env_exclude on establishing step rejected": {
			&RecipeStepIntercept{Command: "curl x", Targetless: true, Cluster: "c", Namespace: "n", EnvExclude: []string{"B"}},
			"env_include/env_exclude require env mode",
		},
		"session_step with mode and cluster set": {
			&RecipeStepIntercept{SessionStep: "a", Script: "run.sh", Mode: []string{"egress"}, Cluster: "c"},
			"belongs on the establishing step",
		},
		"targetless without cluster and namespace": {
			&RecipeStepIntercept{Command: "curl x", Targetless: true},
			"targetless requires cluster and namespace",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := validateInterceptStep(StepValidateCtx{}, tc.intercept)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestParseRemoteRecipe_interceptOk(t *testing.T) {
	const src = `
recipe: {
	name: "intercept-test"
	steps: [
		{
			host: "_"
			intercept: {
				mode:       ["egress"]
				targetless: true
				cluster:    "staging"
				namespace:  "checkout"
				command:    "npm test"
				udp:        true
				output:     "intercept_result"
			}
		},
	]
}
`
	r, err := ParseRemoteRecipe([]byte(src), nil)
	if err != nil {
		t.Fatalf("failed to parse valid intercept recipe: %v", err)
	}
	if len(r.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(r.Steps))
	}
	step, ok := r.Steps[0].Step.(*InterceptStep)
	if !ok {
		t.Fatalf("expected *InterceptStep, got %T", r.Steps[0].Step)
	}
	ic := step.Intercept
	if ic == nil {
		t.Fatal("intercept block not parsed")
	}
	if !ic.Targetless || ic.Cluster != "staging" || ic.Namespace != "checkout" {
		t.Errorf("targetless/cluster/namespace wrong: %+v", ic)
	}
	if ic.Command != "npm test" || !ic.UDP {
		t.Errorf("command/udp wrong: %+v", ic)
	}
	if len(ic.Mode) != 1 || ic.Mode[0] != "egress" {
		t.Errorf("mode wrong: %v", ic.Mode)
	}
	if ic.Output != "intercept_result" {
		t.Errorf("output wrong: %q", ic.Output)
	}
}

func TestParseRemoteRecipe_interceptSessionStepOk(t *testing.T) {
	const src = `
recipe: {
	name: "intercept-session"
	type: "graph"
	steps: [
		{
			id:   "establish"
			host: "_"
			intercept: {targetless: true, cluster: "staging", namespace: "checkout", command: "npm test"}
		},
		{
			id:   "reuse"
			host: "_"
			intercept: {session_step: "establish", script: "run-suite.sh"}
		},
	]
}
`
	r, err := ParseRemoteRecipe([]byte(src), nil)
	if err != nil {
		t.Fatalf("failed to parse session_step recipe: %v", err)
	}
	reuse, ok := r.Steps[1].Step.(*InterceptStep)
	if !ok {
		t.Fatalf("expected *InterceptStep, got %T", r.Steps[1].Step)
	}
	if reuse.Intercept.SessionStep != "establish" {
		t.Fatalf("session_step not parsed: %+v", reuse.Intercept)
	}
}

func TestParseRemoteRecipe_interceptBadModeRejected(t *testing.T) {
	const src = `
recipe: {
	name: "intercept-bad-mode"
	steps: [{host: "_", intercept: {command: "npm test", mode: ["incoming"], targetless: true, cluster: "c", namespace: "n"}}]
}
`
	if _, err := ParseRemoteRecipe([]byte(src), nil); err == nil {
		t.Fatal("expected a validation error for mode: incoming, got nil")
	}
}
