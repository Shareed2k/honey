package cuetry

import (
	"strings"
	"testing"
)

func TestParseRemoteRecipe_aiNotRestrictedToLast(t *testing.T) {
	cue := `
recipe: {
	name: "t"
	steps: [
		{ host: "_", ai: { prompt: "x" } },
		{ host: "*", command: "echo hi" },
	]
}
`
	_, err := ParseRemoteRecipe([]byte(cue), nil)
	if err != nil {
		t.Fatalf("expected ai step to be allowed anywhere, got: %v", err)
	}
}

func TestParseRemoteRecipe_aiCanBeFirstStep(t *testing.T) {
	cue := `
recipe: {
	name: "t"
	steps: [
		{ host: "_", ai: { prompt: "x" } },
	]
}
`
	_, err := ParseRemoteRecipe([]byte(cue), nil)
	if err != nil {
		t.Fatalf("expected ai step to be allowed as the only/first step, got: %v", err)
	}
}

func TestParseRemoteRecipe_aiWrongHost(t *testing.T) {
	cue := `
recipe: {
	name: "t"
	steps: [
		{ host: "*", ai: { prompt: "x" } },
	]
}
`
	_, err := ParseRemoteRecipe([]byte(cue), nil)
	if err == nil || !strings.Contains(err.Error(), `host must be "_"`) {
		t.Fatalf("expected host error, got %v", err)
	}
}

func TestParseRemoteRecipe_aiPromptRequired(t *testing.T) {
	cue := `
recipe: {
	name: "t"
	steps: [
		{ host: "_", ai: { prompt: "" } },
	]
}
`
	_, err := ParseRemoteRecipe([]byte(cue), nil)
	if err == nil || !strings.Contains(err.Error(), "prompt is required") {
		t.Fatalf("expected prompt-required error, got %v", err)
	}
}

func TestParseRemoteRecipe_aiWithEnvRejected(t *testing.T) {
	cue := `
recipe: {
	name: "t"
	steps: [
		{ host: "_", env: { FOO: "bar" }, ai: { prompt: "x" } },
	]
}
`
	_, err := ParseRemoteRecipe([]byte(cue), nil)
	if err == nil || !strings.Contains(err.Error(), "env is not supported") {
		t.Fatalf("expected env error, got %v", err)
	}
}

func TestParseRemoteRecipe_aiTemplatedBadSyntax(t *testing.T) {
	cue := `
recipe: {
	name: "t"
	steps: [
		{ host: "_", templated: true, ai: { prompt: "{{ .Unclosed" } },
	]
}
`
	_, err := ParseRemoteRecipe([]byte(cue), nil)
	if err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("expected a template parse error, got %v", err)
	}
}
