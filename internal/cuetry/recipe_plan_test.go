package cuetry

import "testing"

func TestPreviewForStepTunnel(t *testing.T) {
	kind, err := ClassifyStep(RecipeStep{
		Host: "*",
		Tunnel: &RecipeStepTunnel{
			RemoteHost: "db.internal",
			RemotePort: 5432,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := previewForStep(kind, RecipeStep{
		Tunnel: &RecipeStepTunnel{RemoteHost: "db.internal", RemotePort: 5432},
	})
	want := `tunnel local -> db.internal:5432`
	if got != want {
		t.Fatalf("preview = %q, want %q", got, want)
	}
}

func TestRetrySummaryExplicit(t *testing.T) {
	step := RecipeStep{
		Retry: &RecipeStepRetry{Attempts: 5, Backoff: "exponential"},
	}
	got := retrySummary(step, nil)
	if got != "attempts=5 backoff=exponential" {
		t.Fatalf("retrySummary = %q", got)
	}
}

func TestRetrySummaryAbsent(t *testing.T) {
	if retrySummary(RecipeStep{}, nil) != "" {
		t.Fatal("expected empty retry summary without step.retry")
	}
}
