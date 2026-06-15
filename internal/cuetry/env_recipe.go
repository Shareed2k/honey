package cuetry

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/shareed2k/honey/internal/cuetry/secrets/stack"
	"github.com/shareed2k/honey/internal/hosts"
)

const (
	maxEnvValueLen           = 8192
	maxSecretRefLen          = 65536
	maxSecretRefDisplayRunes = 120
)

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

// OverlapEnvSecrets returns an error if the same key appears in both env and secrets maps.
func OverlapEnvSecrets(env, secrets map[string]string) error {
	if len(env) == 0 || len(secrets) == 0 {
		return nil
	}
	for k := range secrets {
		if _, ok := env[k]; ok {
			return fmt.Errorf("env and secrets both define key %q", k)
		}
	}
	return nil
}

// ValidateRecipeSecretsRefMap checks secret map keys and ref strings (refs are resolved at execute time).
func ValidateRecipeSecretsRefMap(m map[string]string) error {
	return ValidateRecipeSecretsRefMapPrefixes(m, nil)
}

// ValidateRecipeSecretsRefMapPrefixes allows secure:v1 refs and optional plugin-registered prefixes.
func ValidateRecipeSecretsRefMapPrefixes(m map[string]string, allowedPrefixes []string) error {
	for k, ref := range m {
		if err := validateOneSecretRef(k, ref, allowedPrefixes); err != nil {
			return err
		}
	}
	return nil
}

func validateOneSecretRef(k, ref string, allowedPrefixes []string) error {
	if strings.TrimSpace(k) == "" {
		return fmt.Errorf("secrets: empty key")
	}
	if !envNamePattern.MatchString(k) {
		return fmt.Errorf("secrets key %q must match %s (POSIX-style names)", k, envNamePattern.String())
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return fmt.Errorf("secrets: empty ref for key %q", k)
	}
	if strings.ContainsAny(ref, "\x00\n\r") {
		return fmt.Errorf("secrets ref for %q must not contain NUL, LF, or CR", k)
	}
	if len(ref) > maxSecretRefLen {
		return fmt.Errorf("secrets ref for %q exceeds %d bytes", k, maxSecretRefLen)
	}
	if stack.ValidateSecureRef(ref) == nil {
		return nil
	}
	for _, p := range allowedPrefixes {
		if p != "" && strings.HasPrefix(ref, p) {
			return nil
		}
	}
	return fmt.Errorf("secrets ref for key %q: must be secure:v1:… or a registered plugin prefix", k)
}

// RedactedSecretValueForDryRun returns a safe placeholder for dry-run / plans (truncated ref, never resolved material).
func RedactedSecretValueForDryRun(ref string) string {
	ref = strings.TrimSpace(ref)
	const prefix = "<<secret "
	const suffix = ">>"
	maxInner := maxSecretRefDisplayRunes - utf8.RuneCountInString(prefix) - utf8.RuneCountInString(suffix)
	if maxInner < 8 {
		return prefix + "…" + suffix
	}
	runes := []rune(ref)
	if len(runes) <= maxInner {
		return prefix + string(runes) + suffix
	}
	return prefix + string(runes[:maxInner-1]) + "…" + suffix
}

func mergeEnvInto(ctx context.Context, resolve bool, resolver SecretResolver, dst map[string]string, m map[string]string, label string) error {
	for k, v := range m {
		if err := validateOneEnv(k, v); err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		if resolver != nil && resolver.Handles(v) {
			if resolve {
				resolved, err := resolver.Resolve(ctx, v)
				if err != nil {
					return fmt.Errorf("%s key %q: %w", label, k, err)
				}
				v = resolved
			} else {
				v = RedactedSecretValueForDryRun(v)
			}
		}
		dst[k] = v
	}
	return nil
}

// MergeResolvedSecretsInto validates secret refs and merges resolved values into dst (or redacted placeholders when resolve is false).
func MergeResolvedSecretsInto(ctx context.Context, resolve bool, resolver SecretResolver, dst map[string]string, secrets map[string]string, label string) error {
	if len(secrets) == 0 {
		return nil
	}
	for k, ref := range secrets {
		if strings.TrimSpace(k) == "" {
			return fmt.Errorf("%s: empty secret key", label)
		}
		if strings.TrimSpace(ref) == "" {
			return fmt.Errorf("%s: empty ref for key %q", label, k)
		}
		var v string
		var err error
		if resolve {
			if resolver == nil {
				return fmt.Errorf("%s: secret resolver is nil", label)
			}
			v, err = resolver.Resolve(ctx, ref)
			if err != nil {
				return fmt.Errorf("%s key %q: %w", label, k, err)
			}
		} else {
			v = RedactedSecretValueForDryRun(ref)
		}
		if err := validateOneEnv(k, v); err != nil {
			return fmt.Errorf("%s resolved %q: %w", label, k, err)
		}
		dst[k] = v
	}
	return nil
}

// EffectiveEnv merges recipe.defaults.env with step.env (step wins on duplicate keys). Literal env only (no secrets).
func EffectiveEnv(step *StepBase, defaults *RecipeDefaults) (map[string]string, error) {
	out := make(map[string]string)
	if defaults != nil && len(defaults.Env) > 0 {
		if err := mergeEnvInto(context.Background(), false, nil, out, defaults.Env, "defaults.env"); err != nil {
			return nil, err
		}
	}
	if err := mergeEnvInto(context.Background(), false, nil, out, step.Env, "env"); err != nil {
		return nil, err
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

func sanitizeEnvKey(k string) string {
	return regexp.MustCompile(`[^a-zA-Z0-9_]`).ReplaceAllString(strings.ToUpper(k), "_")
}

func appendHostEnvVars(dst map[string]string, r *hosts.Record) {
	if r == nil {
		return
	}
	dst["HONEY_HOST_NAME"] = r.Name
	dst["HONEY_HOST_PRIMARY_IP"] = r.PrimaryIP
	dst["HONEY_HOST_PROVIDER"] = r.Provider
	if r.Zone != "" {
		dst["HONEY_HOST_ZONE"] = r.Zone
	}
	if r.Region != "" {
		dst["HONEY_HOST_REGION"] = r.Region
	}
	for k, v := range r.Meta {
		key := "HONEY_HOST_META_" + sanitizeEnvKey(k)
		if err := validateOneEnv(key, v); err == nil {
			dst[key] = v
		}
	}
	for k, v := range r.Vars {
		suffix := sanitizeEnvKey(k)
		if suffix == "" {
			continue
		}
		key := "HONEY_VAR_" + suffix
		val := v.String()
		if err := validateOneEnv(key, val); err == nil {
			dst[key] = val
		}
	}
}

// EffectiveEnvForRun merges defaults.env → resolved defaults.secrets → step.env → resolved step.secrets → cliEnv → host HONEY_HOST_*.
// When resolveSecrets is false (dry-run / plan), secret values are replaced with redacted placeholders and resolver may be nil.
func EffectiveEnvForRun(ctx context.Context, resolveSecrets bool, resolver SecretResolver, step *StepBase, defaults *RecipeDefaults, cliEnv map[string]string, r *hosts.Record) (map[string]string, error) {
	merged := make(map[string]string)
	if defaults != nil {
		if err := mergeEnvInto(ctx, resolveSecrets, resolver, merged, defaults.Env, "defaults.env"); err != nil {
			return nil, err
		}
		if err := MergeResolvedSecretsInto(ctx, resolveSecrets, resolver, merged, defaults.Secrets, "defaults.secrets"); err != nil {
			return nil, err
		}
	}
	if err := mergeEnvInto(ctx, resolveSecrets, resolver, merged, step.Env, "step.env"); err != nil {
		return nil, err
	}
	if err := MergeResolvedSecretsInto(ctx, resolveSecrets, resolver, merged, step.Secrets, "step.secrets"); err != nil {
		return nil, err
	}
	for k, v := range cliEnv {
		if err := validateOneEnv(k, v); err != nil {
			return nil, fmt.Errorf("CLI env: %w", err)
		}
		merged[k] = v
	}
	appendHostEnvVars(merged, r)
	return merged, nil
}

// EffectiveEnvForRunWithVarExpand merges env then expands ${VAR} in values using merged map as vars.
func EffectiveEnvForRunWithVarExpand(ctx context.Context, resolveSecrets bool, resolver SecretResolver, step *StepBase, defaults *RecipeDefaults, cliEnv map[string]string, r *hosts.Record, strict bool) (map[string]string, error) {
	merged, err := EffectiveEnvForRun(ctx, resolveSecrets, resolver, step, defaults, cliEnv, r)
	if err != nil {
		return nil, err
	}
	if err := ExpandRecipeEnvValues(merged, merged, strict); err != nil {
		return nil, err
	}
	return merged, nil
}

// EffectiveEnvForRemoteHook merges defaults env/secrets, step env/secrets, hook env/secrets, then cliEnv, then host variables.
func EffectiveEnvForRemoteHook(ctx context.Context, resolveSecrets bool, resolver SecretResolver, step *StepBase, defaults *RecipeDefaults, hook *RecipeStepHook, cliEnv map[string]string, r *hosts.Record) (map[string]string, error) {
	if hook == nil {
		return EffectiveEnvForRun(ctx, resolveSecrets, resolver, step, defaults, cliEnv, r)
	}
	merged := make(map[string]string)
	if defaults != nil {
		if err := mergeEnvInto(ctx, resolveSecrets, resolver, merged, defaults.Env, "defaults.env"); err != nil {
			return nil, err
		}
		if err := MergeResolvedSecretsInto(ctx, resolveSecrets, resolver, merged, defaults.Secrets, "defaults.secrets"); err != nil {
			return nil, err
		}
	}
	if err := mergeEnvInto(ctx, resolveSecrets, resolver, merged, step.Env, "step.env"); err != nil {
		return nil, err
	}
	if err := MergeResolvedSecretsInto(ctx, resolveSecrets, resolver, merged, step.Secrets, "step.secrets"); err != nil {
		return nil, err
	}
	if err := mergeEnvInto(ctx, resolveSecrets, resolver, merged, hook.Env, "hooks.env"); err != nil {
		return nil, err
	}
	if err := MergeResolvedSecretsInto(ctx, resolveSecrets, resolver, merged, hook.Secrets, "hooks.secrets"); err != nil {
		return nil, err
	}
	for k, v := range cliEnv {
		if err := validateOneEnv(k, v); err != nil {
			return nil, fmt.Errorf("CLI env: %w", err)
		}
		merged[k] = v
	}
	appendHostEnvVars(merged, r)
	return merged, nil
}

// EffectiveEnvHostOnly returns only HONEY_HOST_* variables derived from r (no recipe env or secrets).
func EffectiveEnvHostOnly(r *hosts.Record) (map[string]string, error) {
	return EffectiveEnvForRun(context.Background(), false, nil, &StepBase{}, nil, nil, r)
}

// EnvForDockerInteractive returns a small env slice for docker exec TTY sessions.
// Full EffectiveEnvForRun includes every meta label and can exceed Engine limits or break shells.
func EnvForDockerInteractive(r *hosts.Record) ([]string, error) {
	if r == nil {
		return nil, nil
	}
	m := map[string]string{
		"HONEY_HOST_NAME":     r.Name,
		"HONEY_HOST_PROVIDER": r.Provider,
	}
	if ip := strings.TrimSpace(r.PrimaryIP); ip != "" {
		m["HONEY_HOST_PRIMARY_IP"] = ip
	}
	return EnvMapForDockerExec(m)
}

// EnvMapForDockerExec formats env for Moby ExecCreateOptions.Env (KEY=value entries).
func EnvMapForDockerExec(env map[string]string) ([]string, error) {
	if len(env) == 0 {
		return nil, nil
	}
	if err := ValidateRecipeEnvMap(env); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out, nil
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
