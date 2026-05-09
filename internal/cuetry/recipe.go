package cuetry

import (
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/errors"

	"github.com/shareed2k/honey/internal/hosts"
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
	agent_transfer?: close({
		dest_host:    string
		source_path:  string
		dest_path:    string
		cloud: close({
			provider: string
			bucket:   string
			prefix?:  string
			object?:  string
			region?:  string
			endpoint?: string
		})
		cloud_backend_ref?: close({
			kind:  string
			name?:  string
			index?: int
		})
		keep_object?:      bool
		max_retries?:      int
		agent_remote_dir?: string
	})
	env?: {[string]: string}
})
#Recipe: close({
	name:  string
	defaults?: close({
		run_as?: string
		env?: {[string]: string}
		k8s_debug_image?: string
	})
	steps: [...#Step]
})
`

func compileAndUnifyRecipe(cueBytes []byte, records []hosts.Record) (cue.Value, error) {
	ctx := cuecontext.New()
	schema := ctx.CompileString(schemaSource)
	if err := schema.Err(); err != nil {
		return cue.Value{}, fmt.Errorf("cuetry: internal schema: %w", err)
	}

	var user cue.Value
	if len(records) > 0 {
		recordsVal := ctx.Encode(records)
		scope := ctx.CompileString("").FillPath(cue.ParsePath("hosts"), recordsVal)
		user = ctx.CompileBytes(cueBytes, cue.Filename("recipe.cue"), cue.Scope(scope))
	} else {
		user = ctx.CompileBytes(cueBytes, cue.Filename("recipe.cue"))
	}

	if err := user.Err(); err != nil {
		return cue.Value{}, fmt.Errorf("cuetry: parse: %w", formatCueErr(err))
	}

	recipe := user.LookupPath(cue.ParsePath("recipe"))
	if !recipe.Exists() {
		return cue.Value{}, fmt.Errorf("cuetry: missing top-level field \"recipe\"")
	}

	def := schema.LookupPath(cue.ParsePath("#Recipe"))
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
func ParseRemoteRecipe(cueBytes []byte, records []hosts.Record) (Recipe, error) {
	var out Recipe
	unified, err := compileAndUnifyRecipe(cueBytes, records)
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
	if out.Defaults != nil && len(out.Defaults.Env) > 0 {
		if err := ValidateRecipeEnvMap(out.Defaults.Env); err != nil {
			return out, fmt.Errorf("cuetry: defaults.env: %w", err)
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
		if len(s.Env) > 0 && (kind == StepKindPut || kind == StepKindGet || kind == StepKindAgentTransfer) {
			return out, fmt.Errorf("cuetry: steps[%d]: env is only supported for command and script steps", i)
		}
		if len(s.Env) > 0 {
			if err := ValidateRecipeEnvMap(s.Env); err != nil {
				return out, fmt.Errorf("cuetry: steps[%d].env: %w", i, err)
			}
		}
		if err := ValidateStepRunAsForKind(kind, s); err != nil {
			return out, fmt.Errorf("cuetry: steps[%d]: %w", i, err)
		}
		if strings.TrimSpace(s.RunAs) != "" {
			if err := ValidateRunAsUser(s.RunAs); err != nil {
				return out, fmt.Errorf("cuetry: steps[%d].run_as: %w", i, err)
			}
		}
		if kind == StepKindAgentTransfer {
			if err := validateAgentTransferStep(i, s, records); err != nil {
				return out, err
			}
		}
	}
	return out, nil
}

func validateAgentTransferStep(i int, s RecipeStep, records []hosts.Record) error {
	at := s.AgentTransfer
	if at == nil {
		return fmt.Errorf("cuetry: steps[%d]: internal agent_transfer", i)
	}
	if err := ValidateHostField(at.DestHost); err != nil {
		return fmt.Errorf("cuetry: steps[%d].agent_transfer.dest_host: %w", i, err)
	}
	if strings.TrimSpace(at.SourcePath) == "" {
		return fmt.Errorf("cuetry: steps[%d].agent_transfer.source_path is empty", i)
	}
	if strings.TrimSpace(at.DestPath) == "" {
		return fmt.Errorf("cuetry: steps[%d].agent_transfer.dest_path is empty", i)
	}
	if at.Cloud == nil {
		return fmt.Errorf("cuetry: steps[%d].agent_transfer.cloud is required", i)
	}
	if strings.TrimSpace(at.Cloud.Provider) == "" {
		return fmt.Errorf("cuetry: steps[%d].agent_transfer.cloud.provider is empty", i)
	}
	if strings.TrimSpace(at.Cloud.Bucket) == "" {
		return fmt.Errorf("cuetry: steps[%d].agent_transfer.cloud.bucket is empty", i)
	}
	if len(records) == 0 {
		return nil
	}
	src, err := ExpandStepHosts(s.Host, records)
	if err != nil {
		return fmt.Errorf("cuetry: steps[%d].host (source): %w", i, err)
	}
	if len(src) != 1 {
		return fmt.Errorf("cuetry: steps[%d].agent_transfer: need exactly one source host match, got %d (narrow host selector)", i, len(src))
	}
	dst, err := ExpandStepHosts(at.DestHost, records)
	if err != nil {
		return fmt.Errorf("cuetry: steps[%d].agent_transfer.dest_host: %w", i, err)
	}
	if len(dst) != 1 {
		return fmt.Errorf("cuetry: steps[%d].agent_transfer: need exactly one destination host match, got %d (narrow dest_host)", i, len(dst))
	}
	return nil
}

// ValidateRemoteRecipe checks that cueBytes is valid CUE and conforms to #Recipe.
func ValidateRemoteRecipe(cueBytes []byte) error {
	_, err := ParseRemoteRecipe(cueBytes, nil)
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
