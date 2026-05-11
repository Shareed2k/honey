package cuetry

import (
	"fmt"
	"strings"
)

// StepKind describes which action a recipe step performs.
type StepKind int

// StepKind values correspond to exactly one populated field on RecipeStep.
const (
	StepKindCommand StepKind = iota
	StepKindPut
	StepKindGet
	StepKindScript
	StepKindAgentTransfer
	StepKindAI
)

// StepKindLabel returns a short stable name for defaults and logging.
func StepKindLabel(k StepKind) string {
	switch k {
	case StepKindCommand:
		return "command"
	case StepKindPut:
		return "put"
	case StepKindGet:
		return "get"
	case StepKindScript:
		return "script"
	case StepKindAgentTransfer:
		return "agent_transfer"
	case StepKindAI:
		return "ai"
	default:
		return "unknown"
	}
}

// ClassifyStep returns the step kind after validating exactly one of command / put / get / script / agent_transfer / ai.
func ClassifyStep(s RecipeStep) (StepKind, error) {
	cmd := strings.TrimSpace(s.Command)
	hasPut := s.Put != nil
	hasGet := s.Get != nil
	hasScript := s.Script != nil
	hasAgent := s.AgentTransfer != nil
	hasAI := s.AI != nil
	n := 0
	if cmd != "" {
		n++
	}
	if hasPut {
		n++
	}
	if hasGet {
		n++
	}
	if hasScript {
		n++
	}
	if hasAgent {
		n++
	}
	if hasAI {
		n++
	}
	if n == 0 {
		return 0, fmt.Errorf("need exactly one of command, put, get, script, agent_transfer, or ai")
	}
	if n > 1 {
		return 0, fmt.Errorf("only one of command, put, get, script, agent_transfer, ai allowed")
	}
	if hasPut {
		if err := validateFileTransfer("put", s.Put); err != nil {
			return 0, err
		}
		return StepKindPut, nil
	}
	if hasGet {
		if err := validateFileTransfer("get", s.Get); err != nil {
			return 0, err
		}
		return StepKindGet, nil
	}
	if hasScript {
		if err := validateFileTransfer("script", s.Script); err != nil {
			return 0, err
		}
		return StepKindScript, nil
	}
	if hasAgent {
		return StepKindAgentTransfer, nil
	}
	if hasAI {
		return StepKindAI, nil
	}
	return StepKindCommand, nil
}

func validateFileTransfer(label string, op *RecipeFileTransfer) error {
	if strings.TrimSpace(op.Local) == "" {
		return fmt.Errorf("%s.local is empty", label)
	}
	if strings.TrimSpace(op.Remote) == "" {
		return fmt.Errorf("%s.remote is empty", label)
	}
	return nil
}

// ValidateStepRunAsForKind rejects per-step run_as on put/get (SFTP only).
// Script steps allow run_as for the execute phase; defaults.run_as applies there too.
func ValidateStepRunAsForKind(kind StepKind, step RecipeStep) error {
	if (kind == StepKindPut || kind == StepKindGet || kind == StepKindAgentTransfer || kind == StepKindAI) && strings.TrimSpace(step.RunAs) != "" {
		return fmt.Errorf("run_as on put/get/agent_transfer/ai steps is not supported (use --ssh-user)")
	}
	return nil
}
