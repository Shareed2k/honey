package cuetry

import (
	"strings"
	"testing"
)

func TestParseRemoteRecipe_controllerOk(t *testing.T) {
	const src = `
recipe: {
	name: "controller-ok"
	type: "controller"
	controller: {model: "gpt-4o", max_turns: 10}
	tasks: [
		{name: "reported", description: "server time and user reported"},
	]
	steps: [
		{id: "time", description: "report the server time", host: "_", command: "date"},
		{id: "user", description: "report the current user", host: "_", command: "whoami"},
	]
}
`
	r, err := ParseRemoteRecipe([]byte(src), nil)
	if err != nil {
		t.Fatalf("valid controller recipe failed to parse: %v", err)
	}
	mode, err := RecipeExecutionMode(r)
	if err != nil || mode != ExecutionModeController {
		t.Fatalf("mode = %v err = %v, want controller", mode, err)
	}
	if len(r.Tasks) != 1 || r.Tasks[0].Name != "reported" {
		t.Fatalf("tasks not parsed: %+v", r.Tasks)
	}
	if r.Controller == nil || r.Controller.Model != "gpt-4o" || r.Controller.MaxTurns != 10 {
		t.Fatalf("controller block not parsed: %+v", r.Controller)
	}
	if r.Steps[0].Step.Base().Description != "report the server time" {
		t.Fatalf("step description not parsed: %q", r.Steps[0].Step.Base().Description)
	}
}

func TestParseRemoteRecipe_controllerRejections(t *testing.T) {
	cases := map[string]struct {
		src  string
		want string
	}{
		"no tasks": {`
recipe: { name: "x", type: "controller"
	steps: [{id: "a", host: "_", command: "date"}] }`, "requires at least one task"},

		"missing step id": {`
recipe: { name: "x", type: "controller"
	tasks: [{name: "t", description: "d"}]
	steps: [{host: "_", command: "date"}] }`, "id is required in controller mode"},

		"depends not allowed": {`
recipe: { name: "x", type: "controller"
	tasks: [{name: "t", description: "d"}]
	steps: [
		{id: "a", host: "_", command: "date"},
		{id: "b", host: "_", depends: ["a"], command: "whoami"},
	] }`, "depends is not allowed in controller mode"},

		"intercept session_step not allowed": {`
recipe: { name: "x", type: "controller"
	tasks: [{name: "t", description: "d"}]
	steps: [
		{id: "a", host: "_", intercept: {command: "curl x", targetless: true, cluster: "c", namespace: "n"}},
		{id: "b", host: "_", intercept: {session_step: "a", script: "run.sh"}},
	] }`, "intercept.session_step is not allowed in controller mode"},

		"duplicate id": {`
recipe: { name: "x", type: "controller"
	tasks: [{name: "t", description: "d"}]
	steps: [
		{id: "a", host: "_", command: "date"},
		{id: "a", host: "_", command: "whoami"},
	] }`, "duplicate step id"},

		"bad type": {`
recipe: { name: "x", type: "orchestrator"
	steps: [{host: "_", command: "date"}] }`, `must match`}, // CUE schema rejects the disjunction first
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseRemoteRecipe([]byte(tc.src), nil)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			// "bad type" is rejected by the CUE schema (disjunction) with a different
			// message; just require an error there.
			if tc.want != "must match" && !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want containing %q", err.Error(), tc.want)
			}
		})
	}
}
