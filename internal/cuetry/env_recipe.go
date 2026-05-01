package cuetry

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const maxEnvValueLen = 8192

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidateRecipeEnvMap checks every key/value pair for safe use in POSIX export assignments.
func ValidateRecipeEnvMap(m map[string]string) error {
	for k, v := range m {
		if err := validateOneEnv(k, v); err != nil {
			return err
		}
	}
	return nil
}

func validateOneEnv(k, v string) error {
	if strings.TrimSpace(k) == "" {
		return fmt.Errorf("env: empty key")
	}
	if !envNamePattern.MatchString(k) {
		return fmt.Errorf("env key %q must match %s (POSIX-style names)", k, envNamePattern.String())
	}
	if strings.ContainsAny(v, "\x00\n\r") {
		return fmt.Errorf("env value for %q must not contain NUL, LF, or CR", k)
	}
	if len(v) > maxEnvValueLen {
		return fmt.Errorf("env value for %q exceeds %d bytes", k, maxEnvValueLen)
	}
	return nil
}

// EffectiveEnv merges recipe.defaults.env with step.env (step wins on duplicate keys).
func EffectiveEnv(step RecipeStep, defaults *RecipeDefaults) (map[string]string, error) {
	out := make(map[string]string)
	if defaults != nil && len(defaults.Env) > 0 {
		for k, v := range defaults.Env {
			if err := validateOneEnv(k, v); err != nil {
				return nil, fmt.Errorf("defaults.env: %w", err)
			}
			out[k] = v
		}
	}
	for k, v := range step.Env {
		if err := validateOneEnv(k, v); err != nil {
			return nil, fmt.Errorf("env: %w", err)
		}
		out[k] = v
	}
	return out, nil
}

// ParseEnvKeyValuePairs parses repeated "KEY=value" strings (first '=' separates key from value).
// Empty entries are skipped. Later duplicates overwrite earlier ones.
func ParseEnvKeyValuePairs(pairs []string) (map[string]string, error) {
	out := make(map[string]string)
	for _, raw := range pairs {
		p := strings.TrimSpace(raw)
		if p == "" {
			continue
		}
		i := strings.IndexByte(p, '=')
		if i < 0 {
			return nil, fmt.Errorf("env %q: expected KEY=value", p)
		}
		k := strings.TrimSpace(p[:i])
		v := p[i+1:]
		if err := validateOneEnv(k, v); err != nil {
			return nil, fmt.Errorf("env %q: %w", p, err)
		}
		out[k] = v
	}
	return out, nil
}

// EffectiveEnvForRun merges recipe env (defaults then step) with cliEnv; cliEnv wins on duplicate keys.
func EffectiveEnvForRun(step RecipeStep, defaults *RecipeDefaults, cliEnv map[string]string) (map[string]string, error) {
	out, err := EffectiveEnv(step, defaults)
	if err != nil {
		return nil, err
	}
	if len(cliEnv) == 0 {
		return out, nil
	}
	merged := make(map[string]string, len(out)+len(cliEnv))
	for k, v := range out {
		merged[k] = v
	}
	for k, v := range cliEnv {
		if err := validateOneEnv(k, v); err != nil {
			return nil, fmt.Errorf("CLI env: %w", err)
		}
		merged[k] = v
	}
	return merged, nil
}

// ShellExportPrefixForRemote prepends stable `export KEY='value'; ` assignments before inner (remote shell).
func ShellExportPrefixForRemote(env map[string]string, inner string) (string, error) {
	if len(env) == 0 {
		return inner, nil
	}
	if err := ValidateRecipeEnvMap(env); err != nil {
		return "", err
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString("export ")
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(shellSingleQuoted(env[k]))
		b.WriteString("; ")
	}
	b.WriteString(inner)
	return b.String(), nil
}
