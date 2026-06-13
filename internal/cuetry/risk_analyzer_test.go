package cuetry

import (
	"testing"
)

func TestAnalyzeRecipeRisk(t *testing.T) {
	r := Recipe{
		Steps: []StepWrapper{
			{Step: &CommandStep{StepBase: StepBase{RunAs: "root"}, Command: "rm -rf /"}},
		},
	}
	report := AnalyzeRecipeRisk(r)
	if report.Score == 0 {
		t.Fatalf("expected non-zero risk score for root rm -rf")
	}
	if report.Level != "Medium" && report.Level != "High" {
		t.Errorf("expected Medium or High risk, got %s", report.Level)
	}
	if len(report.Findings) == 0 {
		t.Errorf("expected findings to be populated")
	}
}
