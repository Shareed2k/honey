package apps

import (
	"fmt"
	"strings"
)

// Validate checks if the AppConfig is well-formed.
func (a AppConfig) Validate() error {
	if strings.TrimSpace(a.Name) == "" {
		return fmt.Errorf("app name is required")
	}

	switch a.Type {
	case AppTypeHTTP, AppTypeTCP:
		// valid
	default:
		return fmt.Errorf("invalid app type %q, must be http or tcp", a.Type)
	}

	if strings.TrimSpace(a.Upstream) == "" {
		return fmt.Errorf("upstream is required")
	}

	if strings.TrimSpace(a.Target) != "" && strings.TrimSpace(a.TargetRegex) != "" {
		return fmt.Errorf("app cannot have both target and target_regex defined")
	}

	if a.LocalPort <= 0 || a.LocalPort > 65535 {
		return fmt.Errorf("invalid local_port %d, must be between 1 and 65535", a.LocalPort)
	}

	return nil
}
