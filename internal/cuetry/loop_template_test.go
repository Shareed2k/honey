package cuetry

import (
	"reflect"
	"strings"
	"testing"
)

func TestRenderLoopTemplate(t *testing.T) {
	tests := []struct {
		name     string
		stepID   string
		stdout   string
		template string
		want     []string
	}{
		{
			name:     "split multiline stdout",
			stepID:   "fetch",
			stdout:   "a\nb\n",
			template: `{{ splitList "\n" (stepStdout "fetch") | compact | toJson }}`,
			want:     []string{"a", "b"},
		},
		{
			name:     "helper stdout lines",
			stepID:   "fetch",
			stdout:   "a\nb\n",
			template: `{{ stepStdoutLines "fetch" | compact | toJson }}`,
			want:     []string{"a", "b"},
		},
		{
			name:     "dashed step id",
			stepID:   "get-controllers",
			stdout:   "10.0.0.1\n",
			template: `{{ stepStdoutLines "get-controllers" | compact | toJson }}`,
			want:     []string{"10.0.0.1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStepOutputStore()
			store.Record(tt.stepID, "h1", tt.stdout)

			got, err := RenderLoopTemplate(RenderLoopTemplateOpts{
				Template: tt.template,
				Store:    store,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRenderLoopTemplate_rejectsNonJSONArray(t *testing.T) {
	store := NewStepOutputStore()
	store.Record("fetch", "h1", "a\nb\n")

	_, err := RenderLoopTemplate(RenderLoopTemplateOpts{
		Template: `{{ stepStdout "fetch" }}`,
		Store:    store,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "loop template must render a JSON array") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRenderLoopTemplate_outputHelpers(t *testing.T) {
	capture := NewRecipeOutputCapture()
	capture.Set("controllers", "a\nb\n")

	got, err := RenderLoopTemplate(RenderLoopTemplateOpts{
		Template: `{{ outputStdoutLines "controllers" | compact | toJson }}`,
		Store:    NewStepOutputStore(),
		Capture:  capture,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("got %+v", got)
	}
}
