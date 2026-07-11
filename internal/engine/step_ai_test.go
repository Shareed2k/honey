package engine

import (
	"bytes"
	"testing"

	"github.com/shareed2k/honey/internal/cuetry"
)

func TestAIExecutor_ExecuteDryRun_ShowsPromptAndModel(t *testing.T) {
	step := &cuetry.AIStep{
		StepBase: cuetry.StepBase{Host: cuetry.MatchLocalAIHost},
		AI:       &cuetry.RecipeAI{Prompt: "Say hello", Model: "gpt-4o-mini"},
	}
	var buf bytes.Buffer
	e := &AIExecutor{}
	err := e.ExecuteDryRun(t.Context(), ExecutionRequest{Index: 0, Step: step}, ExecutionOptions{Execute: false}, &buf)
	if err != nil {
		t.Fatalf("ExecuteDryRun: %v", err)
	}
	out := buf.String()
	if !bytes.Contains([]byte(out), []byte("kind=ai")) {
		t.Fatalf("expected dry-run output to mention kind=ai, got: %s", out)
	}
	if !bytes.Contains([]byte(out), []byte("Say hello")) {
		t.Fatalf("expected dry-run output to preview the prompt, got: %s", out)
	}
	if !bytes.Contains([]byte(out), []byte("gpt-4o-mini")) {
		t.Fatalf("expected dry-run output to show the configured model, got: %s", out)
	}
}

func TestAIExecutor_ExecuteDryRun_NoopWhenExecuting(t *testing.T) {
	step := &cuetry.AIStep{
		StepBase: cuetry.StepBase{Host: cuetry.MatchLocalAIHost},
		AI:       &cuetry.RecipeAI{Prompt: "x"},
	}
	var buf bytes.Buffer
	e := &AIExecutor{}
	err := e.ExecuteDryRun(t.Context(), ExecutionRequest{Index: 0, Step: step}, ExecutionOptions{Execute: true}, &buf)
	if err != nil {
		t.Fatalf("ExecuteDryRun: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected no dry-run output when Execute is true, got: %s", buf.String())
	}
}
