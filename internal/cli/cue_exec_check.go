package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hosts"
)

// runCueExecCheck analyzes every command/script step in a recipe and prints the
// per-step risk decision without executing. With HONEY_POLICY_DIR set it also
// evaluates the command_exec policy. Returns a non-nil error (non-zero exit)
// when any step is a built-in critical pattern or denied by policy.
func runCueExecCheck(ctx context.Context, w io.Writer, recipe cuetry.Recipe, recipeDir string, records []hosts.Record) error {
	enf := checkEnforcer(ctx)
	runner := engine.NewRecipeRunner(engine.RunnerOptions{Enforcer: enf})
	risks := runner.AssessCommandRisk(ctx, engine.RunRequest{
		Recipe:    recipe,
		RecipeDir: recipeDir,
		Records:   records,
		ActorID:   "cli",
	})
	if reportRecipeRisk(w, risks, enf != nil) {
		return fmt.Errorf("recipe risk check failed")
	}
	return nil
}

// reportRecipeRisk prints one block per command/script step and returns whether
// any step was denied — by a built-in critical pattern or by policy. It is pure
// (no enforcer, no env) so the decision logic is unit-testable.
func reportRecipeRisk(w io.Writer, risks []engine.StepRisk, hasPolicy bool) bool {
	if len(risks) == 0 {
		fmt.Fprintln(w, "No command/script steps to check.")
		return false
	}

	var denied bool
	for _, r := range risks {
		fmt.Fprintf(w, "Step %d [%s] host=%s: %s\n", r.StepIndex, r.Kind, r.Host, firstLine(r.Command))
		if r.Analysis.MaxSeverity == "" {
			fmt.Fprintln(w, "  Risk: none")
		} else {
			fmt.Fprintf(w, "  Risk: %s\n", r.Analysis.MaxSeverity)
		}
		for _, s := range r.Analysis.Signals {
			fmt.Fprintf(w, "    - [%s] %s: %s\n", s.Severity, s.ID, s.Reason)
		}

		if r.Analysis.Critical {
			reason := "critical pattern"
			if fc := r.Analysis.FirstCritical(); fc != nil {
				reason = fc.Reason
			}
			fmt.Fprintf(w, "  Decision: DENY (built-in critical: %s)\n", reason)
			denied = true
		}

		if r.Decision != nil {
			denied = reportStepDecision(w, r) || denied
			continue
		}
		if !r.Analysis.Critical && !hasPolicy {
			fmt.Fprintln(w, "  Decision: allow (no policy configured)")
		}
	}
	return denied
}

// reportStepDecision prints a step's OPA verdict and reports whether it denies.
func reportStepDecision(w io.Writer, r engine.StepRisk) bool {
	verdict := r.Decision.Decision
	if verdict == "" {
		verdict = "deny"
		if r.Decision.Allow {
			verdict = "allow"
		}
	}
	line := "  Policy: " + verdict
	if r.Decision.DenyReason != "" {
		line += " — " + r.Decision.DenyReason
	}
	if len(r.Decision.Requires) > 0 {
		line += " (requires: " + strings.Join(r.Decision.Requires, ", ") + ")"
	}
	fmt.Fprintln(w, line)

	switch verdict {
	case "deny", "require_approval", "require_biometric":
		return true
	}
	return !r.Decision.Allow
}

// firstLine returns the first non-empty line of a command for a compact header.
func firstLine(s string) string {
	for line := range strings.SplitSeq(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
