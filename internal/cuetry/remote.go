package cuetry

import (
	"fmt"
	"regexp"
	"strings"
)

var runAsUserPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]{0,31}$`)

// ValidateRunAsUser restricts remote account names to a safe POSIX-like subset
// to avoid shell metacharacters in sudo -u.
func ValidateRunAsUser(user string) error {
	user = strings.TrimSpace(user)
	if user == "" {
		return fmt.Errorf("empty run_as user")
	}
	if !runAsUserPattern.MatchString(user) {
		return fmt.Errorf("run_as %q must match %s", user, runAsUserPattern.String())
	}
	return nil
}

// EffectiveRunAs returns step-level run_as, else recipe defaults.run_as, else "".
func EffectiveRunAs(step RecipeStep, defaults *RecipeDefaults) string {
	if s := strings.TrimSpace(step.RunAs); s != "" {
		return s
	}
	if defaults != nil {
		if s := strings.TrimSpace(defaults.RunAs); s != "" {
			return s
		}
	}
	return ""
}

// WrapRemoteShell runs the inner command as SSH login user; if runAs is set,
// wraps with: sudo -n -u '<runAs>' -- sh -lc '<inner>' (non-interactive sudo).
func WrapRemoteShell(runAs, innerCommand string) (string, error) {
	innerCommand = strings.TrimSpace(innerCommand)
	if innerCommand == "" {
		return "", fmt.Errorf("empty command")
	}
	if runAs == "" {
		return innerCommand, nil
	}
	if err := ValidateRunAsUser(runAs); err != nil {
		return "", err
	}
	escaped := strings.ReplaceAll(innerCommand, `'`, `'\''`)
	return fmt.Sprintf(`sudo -n -u '%s' -- sh -lc '%s'`, runAs, escaped), nil
}

// ScriptRunAfterUpload builds the remote shell command to execute an uploaded file
// with POSIX sh (after SFTP). Optional run_as wraps the run like command steps.
// Optional env is applied as export assignments before `sh remotePath` (same as command steps).
// Scripts should be compatible with `sh` (or rely on a shebang if the kernel honors it when executed as argument to sh — use POSIX sh syntax for portability).
func ScriptRunAfterUpload(remotePath, runAs string, env map[string]string) (string, error) {
	rp := strings.TrimSpace(remotePath)
	if rp == "" {
		return "", fmt.Errorf("empty script remote path")
	}
	inner := "sh " + shellSingleQuoted(rp)
	inner, err := ShellExportPrefixForRemote(env, inner)
	if err != nil {
		return "", err
	}
	return WrapRemoteShell(runAs, inner)
}

func shellSingleQuoted(s string) string {
	return "'" + strings.ReplaceAll(s, `'`, `'\''`) + "'"
}
