package engine

import (
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/cuetry"
)

// newTestTemplateOpts builds an ExecutionOptions with the same store/capture/
// KV wiring renderStepTemplate reads from opts (OutputStore, OutputCapture,
// RecipeKV — see step_executor.go's ExecutionOptions), populated with one
// prior step's stdout, one named output capture, and one KV entry.
func newTestTemplateOpts(t *testing.T) ExecutionOptions {
	t.Helper()
	store := cuetry.NewStepResultStore()
	store.Record("fetch-protected-site", "somehost", `{"title":"Antibot","length":42}`)

	capture := cuetry.NewRecipeOutputCapture()
	capture.Set("RESULT", "captured-value")

	coord := NewRecipeKVCoordinator(0)
	t.Cleanup(coord.Close)
	sess, err := coord.EnsureSession()
	if err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if err := sess.Put("stealth_fetch", `{"title":"Antibot","length":42}`); err != nil {
		t.Fatalf("Put: %v", err)
	}

	return ExecutionOptions{
		OutputStore:   store,
		OutputCapture: capture,
		RecipeKV:      coord,
	}
}

func TestRenderStepTemplate_KVGetAndFromJSON(t *testing.T) {
	t.Parallel()
	opts := newTestTemplateOpts(t)
	got, err := renderStepTemplate(`TITLE="{{ (kvGet "stealth_fetch" | fromJson).title }}"`, opts, nil)
	if err != nil {
		t.Fatalf("renderStepTemplate: %v", err)
	}
	if got != `TITLE="Antibot"` {
		t.Fatalf("got %q", got)
	}
}

func TestRenderStepTemplate_KVHas(t *testing.T) {
	t.Parallel()
	opts := newTestTemplateOpts(t)
	got, err := renderStepTemplate(`{{ if kvHas "stealth_fetch" }}present{{ else }}missing{{ end }}`, opts, nil)
	if err != nil {
		t.Fatalf("renderStepTemplate: %v", err)
	}
	if got != "present" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderStepTemplate_StepStdout(t *testing.T) {
	t.Parallel()
	opts := newTestTemplateOpts(t)
	got, err := renderStepTemplate(`{{ stepStdout "fetch-protected-site" }}`, opts, nil)
	if err != nil {
		t.Fatalf("renderStepTemplate: %v", err)
	}
	if got != `{"title":"Antibot","length":42}` {
		t.Fatalf("got %q", got)
	}
}

func TestRenderStepTemplate_OutputStdout(t *testing.T) {
	t.Parallel()
	opts := newTestTemplateOpts(t)
	got, err := renderStepTemplate(`{{ outputStdout "RESULT" }}`, opts, nil)
	if err != nil {
		t.Fatalf("renderStepTemplate: %v", err)
	}
	if got != "captured-value" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderStepTemplate_EnvAccess(t *testing.T) {
	t.Parallel()
	opts := newTestTemplateOpts(t)
	env := map[string]string{"HONEY_HOST_NAME": "web1"}
	got, err := renderStepTemplate(`host={{ .env.HONEY_HOST_NAME }}`, opts, env)
	if err != nil {
		t.Fatalf("renderStepTemplate: %v", err)
	}
	if got != "host=web1" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderStepTemplate_MissingKVKeyIsEmptyNotError(t *testing.T) {
	t.Parallel()
	opts := newTestTemplateOpts(t)
	got, err := renderStepTemplate(`[{{ kvGet "does_not_exist" }}]`, opts, nil)
	if err != nil {
		t.Fatalf("renderStepTemplate: %v", err)
	}
	if got != "[]" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderStepTemplate_BadSyntaxErrors(t *testing.T) {
	t.Parallel()
	opts := newTestTemplateOpts(t)
	_, err := renderStepTemplate(`{{ .Unclosed`, opts, nil)
	if err == nil {
		t.Fatal("expected a parse error")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Fatalf("expected a parse error, got: %v", err)
	}
}
