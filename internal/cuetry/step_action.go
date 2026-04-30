package cuetry

import (
	"fmt"
	"strings"
)

// StepKind describes which action a recipe step performs.
type StepKind int

const (
	StepKindCommand StepKind = iota
	StepKindPut
	StepKindGet
	StepKindScript
)

// ClassifyStep returns the step kind after validating exactly one of command / put / get / script.
func ClassifyStep(s RecipeStep) (StepKind, error) {
	cmd := strings.TrimSpace(s.Command)
	hasPut := s.Put != nil
	hasGet := s.Get != nil
	hasScript := s.Script != nil
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
	if n == 0 {
		return 0, fmt.Errorf("need exactly one of command, put, get, or script")
	}
	if n > 1 {
		return 0, fmt.Errorf("only one of command, put, get, script allowed")
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
	if (kind == StepKindPut || kind == StepKindGet) && strings.TrimSpace(step.RunAs) != "" {
		return fmt.Errorf("run_as on put/get steps is not supported (file transfer uses --ssh-user)")
	}
	return nil
}
