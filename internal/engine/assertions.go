package engine

import (
	"fmt"
	"regexp"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/tidwall/gjson"
)

// EvaluateAssertions runs step output through the requested rules.
// Mutates result.Success and result.ErrMsg if an assertion fails or overrides.
func EvaluateAssertions(result *HostExecResult, assertions []cuetry.Assertion) error {
	if len(assertions) == 0 {
		return nil
	}

	// ExitCode checks can override a previously failed result.
	for _, a := range assertions {
		if a.ExitCode != nil {
			if result.ExitCode == *a.ExitCode {
				result.Success = true
				result.ErrMsg = "" // clear previous errors if we expected this code
			} else {
				err := fmt.Errorf("assert failed: expected exit code %d, got %d", *a.ExitCode, result.ExitCode)
				result.Success = false
				result.ErrMsg = err.Error()
				return err
			}
		}
	}

	// Only process text assertions if the step is considered successful so far
	if !result.Success {
		return nil
	}

	for _, a := range assertions {
		if a.Regex != "" {
			matched, err := regexp.MatchString(a.Regex, result.Output)
			if err != nil {
				return markFailed(result, fmt.Errorf("invalid regex %q: %w", a.Regex, err))
			}
			if !matched {
				return markFailed(result, fmt.Errorf("assert failed: regex %q did not match output", a.Regex))
			}
		}

		if a.NotRegex != "" {
			matched, err := regexp.MatchString(a.NotRegex, result.Output)
			if err != nil {
				return markFailed(result, fmt.Errorf("invalid not_regex %q: %w", a.NotRegex, err))
			}
			if matched {
				return markFailed(result, fmt.Errorf("assert failed: not_regex %q matched output", a.NotRegex))
			}
		}

		if a.JSONPath != "" {
			if !gjson.ValidBytes([]byte(result.Output)) {
				return markFailed(result, fmt.Errorf("assert failed: json_path %q applied to invalid JSON output", a.JSONPath))
			}
			val := gjson.Get(result.Output, a.JSONPath)
			if !val.Exists() {
				return markFailed(result, fmt.Errorf("assert failed: json_path %q not found in output", a.JSONPath))
			}
			if a.ExpectedValue != "" && val.String() != a.ExpectedValue {
				return markFailed(result, fmt.Errorf("assert failed: json_path %q expected %q, got %q", a.JSONPath, a.ExpectedValue, val.String()))
			}
		}
	}

	return nil
}

func markFailed(result *HostExecResult, err error) error {
	result.Success = false
	if result.ErrMsg != "" {
		result.ErrMsg = result.ErrMsg + "; " + err.Error()
	} else {
		result.ErrMsg = err.Error()
	}
	return err
}
