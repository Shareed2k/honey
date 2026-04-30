package cuetry

import (
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/errors"
)

// schemaSource defines the shape of a "remote recipe" document: a named list of
// host + shell command steps (similar in spirit to a tiny Ansible play).
const schemaSource = `
#Step: close({
	host:     string
	run_as?:  string
	command?: string
	put?: close({
		local:  string
		remote: string
	})
	get?: close({
		local:  string
		remote: string
	})
	script?: close({
		local:  string
		remote: string
	})
})
#Recipe: close({
	name:  string
	defaults?: close({
		run_as?: string
	})
	steps: [...#Step]
})
`

func compileAndUnifyRecipe(cueBytes []byte) (cue.Value, error) {
	ctx := cuecontext.New()
	schema := ctx.CompileString(schemaSource)
	if err := schema.Err(); err != nil {
		return cue.Value{}, fmt.Errorf("cuetry: internal schema: %w", err)
	}

	user := ctx.CompileBytes(cueBytes, cue.Filename("recipe.cue"))
	if err := user.Err(); err != nil {
		return cue.Value{}, fmt.Errorf("cuetry: parse: %w", formatCueErr(err))
	}

	recipe := user.LookupPath(cue.ParsePath("recipe"))
	if !recipe.Exists() {
		return cue.Value{}, fmt.Errorf("cuetry: missing top-level field \"recipe\"")
	}

	def := schema.LookupDef("#Recipe")
	if !def.Exists() {
		return cue.Value{}, fmt.Errorf("cuetry: internal schema missing #Recipe")
	}

	unified := def.Unify(recipe)
	if err := unified.Validate(cue.Concrete(true), cue.Final()); err != nil {
		return cue.Value{}, fmt.Errorf("cuetry: validate: %w", formatCueErr(err))
	}
	if err := unified.Err(); err != nil {
		return cue.Value{}, fmt.Errorf("cuetry: %w", formatCueErr(err))
	}
	return unified, nil
}

// ParseRemoteRecipe validates cueBytes and decodes the recipe into Go values.
func ParseRemoteRecipe(cueBytes []byte) (Recipe, error) {
	var out Recipe
	unified, err := compileAndUnifyRecipe(cueBytes)
	if err != nil {
		return out, err
	}
	if err := unified.Decode(&out); err != nil {
		return out, fmt.Errorf("cuetry: decode: %w", err)
	}
	if out.Defaults != nil && strings.TrimSpace(out.Defaults.RunAs) != "" {
		if err := ValidateRunAsUser(out.Defaults.RunAs); err != nil {
			return out, fmt.Errorf("cuetry: defaults.run_as: %w", err)
		}
	}
	for i, s := range out.Steps {
		if err := ValidateHostField(s.Host); err != nil {
			return out, fmt.Errorf("cuetry: steps[%d].host: %w", i, err)
		}
		kind, err := ClassifyStep(s)
		if err != nil {
			return out, fmt.Errorf("cuetry: steps[%d]: %w", i, err)
		}
		if err := ValidateStepRunAsForKind(kind, s); err != nil {
			return out, fmt.Errorf("cuetry: steps[%d]: %w", i, err)
		}
		if strings.TrimSpace(s.RunAs) != "" {
			if err := ValidateRunAsUser(s.RunAs); err != nil {
				return out, fmt.Errorf("cuetry: steps[%d].run_as: %w", i, err)
			}
		}
	}
	return out, nil
}

// ValidateRemoteRecipe checks that cueBytes is valid CUE and conforms to #Recipe.
func ValidateRemoteRecipe(cueBytes []byte) error {
	_, err := ParseRemoteRecipe(cueBytes)
	return err
}

func formatCueErr(err error) error {
	if err == nil {
		return nil
	}
	var buf strings.Builder
	for _, e := range errors.Errors(err) {
		if buf.Len() > 0 {
			buf.WriteString("; ")
		}
		buf.WriteString(e.Error())
	}
	if buf.Len() == 0 {
		return err
	}
	return fmt.Errorf("%s", buf.String())
}
