package cuetry

import (
	"fmt"
	"strings"
)

// RiskReport represents a summary of destructive or high concurrency operations.
type RiskReport struct {
	Score    int      `json:"score"`
	Level    string   `json:"level"`
	Findings []string `json:"findings"`
}

// AnalyzeRecipeRisk computes a risk score and extracts findings of dangerous steps.
func AnalyzeRecipeRisk(r Recipe) RiskReport {
	score := 0
	var findings []string

	checkCmd := func(cmd string, stepName string) {
		cmd = strings.ToLower(cmd)
		dangerous := []string{"rm -rf", "mkfs", "reboot", "shutdown", "systemctl restart", "systemctl stop", "drop table", "truncate"}
		for _, d := range dangerous {
			if strings.Contains(cmd, d) {
				score += 20
				findings = append(findings, "Step '"+stepName+"' uses destructive command pattern: "+d)
				break
			}
		}
	}

	for i, w := range r.Steps {
		stepName := w.Step.Base().ID
		if stepName == "" {
			stepName = fmt.Sprintf("step-%d", i+1)
		}

		if w.Step.Base().RunAs == "root" || w.Step.Base().RunAs == "sudo" {
			score += 10
			findings = append(findings, "Step '"+stepName+"' runs as root/sudo")
		}

		switch st := w.Step.(type) {
		case *CommandStep:
			checkCmd(st.Command, stepName)
		case *ScriptStep:
			// Just a rough check on local script content if it's inline, but normally it's a file.
			if st.Script != nil {
				checkCmd(st.Script.Local, stepName)
			}
		case *K8sStep:
			if st.K8s != nil && (st.K8s.Delete != nil || st.K8s.RolloutRestart != nil || (st.K8s.Scale != nil && st.K8s.Scale.Replicas == 0)) {
				score += 15
				findings = append(findings, "Step '"+stepName+"' performs a destructive Kubernetes action")
			}
		case *PostgresStep:
			if st.Postgres != nil && (st.Postgres.Action == "exec" || st.Postgres.Action == "migrate") {
				score += 10
				findings = append(findings, "Step '"+stepName+"' performs mutating database operations")
			}
		}
	}

	if r.Defaults != nil && r.Defaults.MaxParallel > 10 {
		score += 5
		findings = append(findings, "Recipe executes with high concurrency (>10)")
	}

	level := "Low"
	if score >= 40 {
		level = "High"
	} else if score >= 20 {
		level = "Medium"
	}

	return RiskReport{
		Score:    score,
		Level:    level,
		Findings: findings,
	}
}
