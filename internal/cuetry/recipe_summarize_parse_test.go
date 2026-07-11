package cuetry

import (
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestParseRemoteRecipe_summarizeLastStep(t *testing.T) {
	cue := `
recipe: {
	name: "t"
	steps: [
		{ host: "*", command: "echo hi" },
		{ host: "_", summarize: { prompt: "Summarize." } },
	]
}
`
	rec := []hosts.Record{{Name: "h1", PrimaryIP: "10.0.0.1", Provider: "gcp"}}
	_, err := ParseRemoteRecipe([]byte(cue), rec)
	if err != nil {
		t.Fatal(err)
	}
}

func TestParseRemoteRecipe_summarizeNotLast(t *testing.T) {
	cue := `
recipe: {
	name: "t"
	steps: [
		{ host: "_", summarize: { prompt: "x" } },
		{ host: "*", command: "echo hi" },
	]
}
`
	_, err := ParseRemoteRecipe([]byte(cue), nil)
	if err == nil || !strings.Contains(err.Error(), "summarize step must be the last") {
		t.Fatalf("expected last-step error, got %v", err)
	}
}

func TestParseRemoteRecipe_summarizeWithEnv(t *testing.T) {
	cue := `
recipe: {
	name: "t"
	steps: [
		{ host: "*", command: "echo hi" },
		{ host: "_", env: { FOO: "bar" }, summarize: { prompt: "x" } },
	]
}
`
	_, err := ParseRemoteRecipe([]byte(cue), nil)
	if err == nil || !strings.Contains(err.Error(), "env is not supported") {
		t.Fatalf("expected env error, got %v", err)
	}
}

func TestParseRemoteRecipe_summarizeWrongHost(t *testing.T) {
	cue := `
recipe: {
	name: "t"
	steps: [
		{ host: "*", command: "echo hi" },
		{ host: "*", summarize: { prompt: "x" } },
	]
}
`
	_, err := ParseRemoteRecipe([]byte(cue), nil)
	if err == nil || !strings.Contains(err.Error(), `host must be "_"`) {
		t.Fatalf("expected host error, got %v", err)
	}
}

func TestParseRemoteRecipe_summarizeFirstStep(t *testing.T) {
	cue := `
recipe: {
	name: "t"
	steps: [
		{ host: "_", summarize: { prompt: "x" } },
	]
}
`
	_, err := ParseRemoteRecipe([]byte(cue), nil)
	if err == nil || !strings.Contains(err.Error(), "cannot be the first step") {
		t.Fatalf("expected first-step error, got %v", err)
	}
}

func TestExpandStepHosts_localAI(t *testing.T) {
	got, err := ExpandStepHosts(MatchLocalAIHost, []hosts.Record{{Name: "x", PrimaryIP: "1.1.1.1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != MatchLocalAIHost {
		t.Fatalf("got %#v", got)
	}
}
