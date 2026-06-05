package cuetry

import "strings"

const maxOutputCaptureBytes = 64 << 10

// RecipeOutputCapture holds rendered stdout keyed by template.output capture names.
type RecipeOutputCapture struct {
	byName map[string]string
}

// NewRecipeOutputCapture creates an empty capture registry.
func NewRecipeOutputCapture() *RecipeOutputCapture {
	return &RecipeOutputCapture{byName: make(map[string]string)}
}

// Set stores trimmed stdout for a capture name.
func (c *RecipeOutputCapture) Set(name, stdout string) {
	if c == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	out := strings.TrimSpace(stdout)
	if len(out) > maxOutputCaptureBytes {
		out = out[:maxOutputCaptureBytes]
	}
	if c.byName == nil {
		c.byName = make(map[string]string)
	}
	c.byName[name] = out
}

// All returns a copy of all capture name → stdout mappings.
func (c *RecipeOutputCapture) All() map[string]string {
	out := make(map[string]string)
	if c == nil || c.byName == nil {
		return out
	}
	for k, v := range c.byName {
		out[k] = v
	}
	return out
}

// Get returns captured stdout for name.
func (c *RecipeOutputCapture) Get(name string) (string, bool) {
	if c == nil || c.byName == nil {
		return "", false
	}
	v, ok := c.byName[strings.TrimSpace(name)]
	return v, ok
}

// View returns template/CEL-friendly named output metadata.
func (c *RecipeOutputCapture) View() map[string]any {
	out := make(map[string]any)
	if c == nil || c.byName == nil {
		return out
	}
	for name, stdout := range c.byName {
		trimmed := strings.TrimSpace(stdout)
		out[name] = map[string]any{
			"stdout":       trimmed,
			"stdout_lines": strings.Split(trimmed, "\n"),
			"succeeded":    true,
			"skipped":      false,
			"exit_code":    int64(0),
		}
	}
	return out
}
