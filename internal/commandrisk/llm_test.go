package commandrisk

import (
	"context"
	"fmt"
	"testing"
)

func TestLLMAdvisor_Advise(t *testing.T) {
	stub := func(_ context.Context, _, _, _ string, _ int) (string, error) {
		return "Here is my assessment:\n```json\n{\"risk\":\"high\",\"reasons\":[\"deletes files\"],\"explanation\":\"rm -rf\"}\n```", nil
	}
	a := NewLLMAdvisor(stub, "test-model")
	adv, err := a.Advise(context.Background(), "rm -rf /tmp/x", Detected{Commands: []string{"rm"}})
	if err != nil {
		t.Fatalf("Advise: %v", err)
	}
	if adv.Risk != SeverityHigh || len(adv.Reasons) != 1 || adv.Reasons[0] != "deletes files" {
		t.Fatalf("advice = %+v", adv)
	}
}

func TestLLMAdvisor_MalformedDegrades(t *testing.T) {
	cases := map[string]CompleteFunc{
		"no json":     func(_ context.Context, _, _, _ string, _ int) (string, error) { return "sorry, no idea", nil },
		"bad json":    func(_ context.Context, _, _, _ string, _ int) (string, error) { return "{not valid}", nil },
		"model error": func(_ context.Context, _, _, _ string, _ int) (string, error) { return "", fmt.Errorf("upstream down") },
	}
	for name, stub := range cases {
		t.Run(name, func(t *testing.T) {
			adv, err := NewLLMAdvisor(stub, "m").Advise(context.Background(), "uptime", Detected{})
			if err == nil || adv != nil {
				t.Fatalf("expected (nil, err), got (%+v, %v)", adv, err)
			}
		})
	}
}

// Advisor is satisfied by *LLMAdvisor (compile-time seam check).
var _ Advisor = (*LLMAdvisor)(nil)
