package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/commandrisk"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/policy"
)

func TestReportRecipeRisk(t *testing.T) {
	tests := []struct {
		name        string
		risks       []engine.StepRisk
		hasPolicy   bool
		wantDenied  bool
		wantContain string
	}{
		{
			name:        "empty",
			risks:       nil,
			wantDenied:  false,
			wantContain: "No command/script steps",
		},
		{
			name: "built-in critical",
			risks: []engine.StepRisk{
				{StepIndex: 0, Kind: "command", Host: "web-1", Command: "rm -rf /", Analysis: commandrisk.Analyze("rm -rf /")},
			},
			wantDenied:  true,
			wantContain: "DENY (built-in critical",
		},
		{
			name: "benign no policy",
			risks: []engine.StepRisk{
				{StepIndex: 0, Kind: "command", Host: "web-1", Command: "uptime", Analysis: commandrisk.Analyze("uptime")},
			},
			hasPolicy:   false,
			wantDenied:  false,
			wantContain: "allow (no policy configured)",
		},
		{
			name: "policy deny",
			risks: []engine.StepRisk{
				{
					StepIndex: 0, Kind: "command", Host: "prod-1", Command: "kubectl delete pod x",
					Analysis: commandrisk.Analyze("kubectl delete pod x"),
					Decision: &policy.Decision{Allow: false, DenyReason: "blocked", Decision: "deny"},
				},
			},
			hasPolicy:   true,
			wantDenied:  true,
			wantContain: "Policy: deny — blocked",
		},
		{
			name: "python interpreter header",
			risks: []engine.StepRisk{
				{StepIndex: 0, Kind: "command", Host: "web-1", Command: "print(\"hi\")", Interpreter: "python3", Analysis: commandrisk.AnalyzeStep("print(\"hi\")", "python3")},
			},
			hasPolicy:   false,
			wantDenied:  false,
			wantContain: "[command:python3]",
		},
		{
			name: "require approval",
			risks: []engine.StepRisk{
				{
					StepIndex: 0, Kind: "command", Host: "prod-1", Command: "systemctl stop nginx",
					Analysis: commandrisk.Analyze("systemctl stop nginx"),
					Decision: &policy.Decision{Allow: false, Decision: "require_approval"},
				},
			},
			hasPolicy:   true,
			wantDenied:  true,
			wantContain: "Policy: require_approval",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			got := reportRecipeRisk(&buf, tt.risks, tt.hasPolicy)
			if got != tt.wantDenied {
				t.Errorf("denied = %v, want %v", got, tt.wantDenied)
			}
			if !strings.Contains(buf.String(), tt.wantContain) {
				t.Errorf("output missing %q:\n%s", tt.wantContain, buf.String())
			}
		})
	}
}
