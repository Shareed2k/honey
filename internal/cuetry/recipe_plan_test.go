package cuetry

import "testing"

func TestPreviewForStepTunnel(t *testing.T) {
	step := &TunnelStep{
		StepBase: StepBase{Host: "*"},
		Tunnel: &RecipeStepTunnel{
			RemoteHost: "db.internal",
			RemotePort: 5432,
		},
	}
	got := previewForStep(step)
	want := `tunnel local -> db.internal:5432`
	if got != want {
		t.Fatalf("preview = %q, want %q", got, want)
	}
}

func TestRetrySummaryExplicit(t *testing.T) {
	step := &StepBase{
		Retry: &RecipeStepRetry{Attempts: 5, Backoff: "exponential"},
	}
	got := retrySummary(step, nil)
	if got != "attempts=5 backoff=exponential" {
		t.Fatalf("retrySummary = %q", got)
	}
}

func TestRetrySummaryAbsent(t *testing.T) {
	if retrySummary(&StepBase{}, nil) != "" {
		t.Fatal("expected empty retry summary without step.retry")
	}
}
