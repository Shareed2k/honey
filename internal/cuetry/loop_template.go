package cuetry

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
)

// RenderLoopTemplateOpts configures dynamic loop item rendering.
type RenderLoopTemplateOpts struct {
	Template string
	Store    *StepResultStore
	Capture  *RecipeOutputCapture
}

// RenderLoopTemplate renders a Sprig-backed template and decodes its JSON array output.
func RenderLoopTemplate(opts RenderLoopTemplateOpts) ([]string, error) {
	data := map[string]any{
		"steps":   opts.Store.StepsTemplateData(),
		"outputs": opts.Capture.View(),
	}
	rendered, err := RenderTemplate(RenderTemplateOpts{
		Template: opts.Template,
		Data:     data,
		Funcs:    LoopTemplateFuncMap(opts.Store, opts.Capture),
	})
	if err != nil {
		return nil, err
	}

	var raw []any
	if err := json.Unmarshal([]byte(strings.TrimSpace(rendered)), &raw); err != nil {
		return nil, fmt.Errorf("cuetry: loop template must render a JSON array: %w", err)
	}

	items := make([]string, 0, len(raw))
	for _, item := range raw {
		items = append(items, formatJQValue(item))
	}
	return items, nil
}

// LoopTemplateFuncMap returns stepStdout/stepStdoutLines/outputStdout/
// outputStdoutLines template functions backed by store and capture — used
// by loop: templates, and by internal/engine's templated command/script
// rendering.
func LoopTemplateFuncMap(store *StepResultStore, capture *RecipeOutputCapture) template.FuncMap {
	out := outputTemplateFuncMap(capture)
	for name, fn := range (template.FuncMap{
		"stepStdout": func(stepID string) string {
			if store == nil {
				return ""
			}
			stdout, ok := store.FirstStdout(stepID)
			if !ok {
				return ""
			}
			return stdout
		},
		"stepStdoutLines": func(stepID string) []string {
			if store == nil {
				return []string{}
			}
			stdout, ok := store.FirstStdout(stepID)
			if !ok {
				return []string{}
			}
			return strings.Split(strings.TrimSpace(stdout), "\n")
		},
	}) {
		out[name] = fn
	}
	return out
}

func outputTemplateFuncMap(capture *RecipeOutputCapture) template.FuncMap {
	return template.FuncMap{
		"outputStdout": func(name string) string {
			if capture == nil {
				return ""
			}
			stdout, ok := capture.Get(name)
			if !ok {
				return ""
			}
			return stdout
		},
		"outputStdoutLines": func(name string) []string {
			if capture == nil {
				return []string{}
			}
			stdout, ok := capture.Get(name)
			if !ok {
				return []string{}
			}
			return strings.Split(strings.TrimSpace(stdout), "\n")
		},
	}
}
