package cuetry

import (
	"fmt"
	"regexp"
	"strings"
)

var recipeVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ExpandRecipeVars replaces ${NAME} in s using vars.
// When strict is true, unknown names return an error; otherwise they are left literal.
func ExpandRecipeVars(s string, vars map[string]string, strict bool) (string, error) {
	if s == "" || !strings.Contains(s, "${") {
		return s, nil
	}
	var errOut error
	out := recipeVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		sub := recipeVarPattern.FindStringSubmatch(match)
		if len(sub) != 2 {
			return match
		}
		name := sub[1]
		if vars == nil {
			if strict {
				errOut = fmt.Errorf("recipe var: undefined %s", match)
			}
			return match
		}
		val, ok := vars[name]
		if !ok {
			if strict {
				errOut = fmt.Errorf("recipe var: undefined %s", match)
			}
			return match
		}
		return val
	})
	return out, errOut
}

// BuildRecipeVarMap merges capture names and env (later keys win).
func BuildRecipeVarMap(capture *RecipeOutputCapture, env map[string]string) map[string]string {
	out := make(map[string]string)
	if capture != nil {
		for k, v := range capture.All() {
			out[k] = v
		}
	}
	for k, v := range env {
		out[k] = v
	}
	return out
}

// ExpandRecipeVarsInData expands ${VAR} in string values of data (top-level and nested maps).
func ExpandRecipeVarsInData(data map[string]any, vars map[string]string, strict bool) error {
	if len(data) == 0 {
		return nil
	}
	for k, v := range data {
		expanded, err := expandRecipeVarValue(v, vars, strict)
		if err != nil {
			return fmt.Errorf("data[%q]: %w", k, err)
		}
		data[k] = expanded
	}
	return nil
}

func expandRecipeVarValue(v any, vars map[string]string, strict bool) (any, error) {
	switch x := v.(type) {
	case string:
		return ExpandRecipeVars(x, vars, strict)
	case map[string]any:
		for k, inner := range x {
			expanded, err := expandRecipeVarValue(inner, vars, strict)
			if err != nil {
				return nil, fmt.Errorf("%q: %w", k, err)
			}
			x[k] = expanded
		}
		return x, nil
	case []any:
		for i, inner := range x {
			expanded, err := expandRecipeVarValue(inner, vars, strict)
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			x[i] = expanded
		}
		return x, nil
	default:
		return v, nil
	}
}

// ExpandRecipeEnvValues expands ${VAR} in env map values (keys unchanged).
func ExpandRecipeEnvValues(env map[string]string, vars map[string]string, strict bool) error {
	for k, v := range env {
		expanded, err := ExpandRecipeVars(v, vars, strict)
		if err != nil {
			return fmt.Errorf("env[%q]: %w", k, err)
		}
		env[k] = expanded
	}
	return nil
}
