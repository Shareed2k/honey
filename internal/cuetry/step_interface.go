package cuetry

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/invopop/jsonschema"
	"github.com/shareed2k/honey/internal/hosts"
)

// Step is one recipe action (command, template, postgres, …). Concrete types embed
// StepBase (cross-cutting fields) and, for remotely-executed kinds, RemoteExec.
//
// The interface is intentionally small: identity (Kind), shared-field access (Base),
// deep-copy for loop fan-out (Clone), and self-validation. Execution lives in
// internal/ui behind its own registry — a method here would create an import cycle
// (internal/ui imports internal/cuetry, not the reverse).
type Step interface {
	Kind() string
	Base() *StepBase
	Clone() Step
	Validate(vc StepValidateCtx) error
}

// RemoteStep is implemented by steps that run against remote hosts and therefore
// carry SSH / fan-out options. Template and AI steps (local-only) do not implement it.
type RemoteStep interface {
	Step
	Remote() *RemoteExec
}

// Step kind identifiers. These replace the old StepKind enum; using strings avoids
// the zero-value enum footgun (iota 0 silently meaning "command").
const (
	KindCommand       = "command"
	KindScript        = "script"
	KindPut           = "put"
	KindGet           = "get"
	KindAgentTransfer = "agent_transfer"
	KindAI            = "ai"
	KindTemplate      = "template"
	KindPlugin        = "plugin"
	KindTunnel        = "tunnel"
	KindK8s           = "k8s"
	KindDocker        = "docker"
	KindOpensearch    = "opensearch"
	KindPostgres      = "postgres"
	KindRecipe        = "recipe"
)

// StepValidateCtx carries everything the per-step validators need. It replaces the
// long positional parameter list of the old validateDecodedRecipeStep.
type StepValidateCtx struct {
	Index          int
	NumSteps       int
	Defaults       *RecipeDefaults
	Records        []hosts.Record
	SecretPrefixes []string
	Mode           ExecutionMode
}

// StepBase holds the cross-cutting fields shared by every step kind. It is embedded
// (anonymously) by each concrete step, so these fields flatten into the step's JSON.
type StepBase struct {
	ID            string              `json:"id,omitempty"`
	Depends       []string            `json:"depends,omitempty"`
	Host          string              `json:"host" jsonschema:"default=*"`
	Env           map[string]string   `json:"env,omitempty"`
	Secrets       map[string]string   `json:"secrets,omitempty"`
	EnvFrom       []EnvFromRef        `json:"env_from,omitempty"`
	RunAs         string              `json:"run_as,omitempty"`
	When          string              `json:"when,omitempty"`
	ChangedWhen   string              `json:"changed_when,omitempty"`
	FailedWhen    string              `json:"failed_when,omitempty"`
	Retry         *RecipeStepRetry    `json:"retry,omitempty"`
	Timeout       string              `json:"timeout,omitempty"`
	IgnoreErrors  bool                `json:"ignore_errors,omitempty" jsonschema:"default=false"`
	CheckCmd      string              `json:"check_cmd,omitempty"`
	Output        string              `json:"output,omitempty"`
	Loop          string              `json:"loop,omitempty"`
	LoopFrom      *RecipeLoop         `json:"loop_from,omitempty"`
	Notify        *RecipeNotify       `json:"notify,omitempty"`
	Hooks         *RecipeStepHooks    `json:"hooks,omitempty"`
	NotifyHandler []string            `json:"notify_handler,omitempty"`
	KVTunnel      *bool               `json:"kv_tunnel,omitempty" jsonschema:"default=false"`
	Matrix        map[string][]string `json:"matrix,omitempty"`
	Assert        []Assertion         `json:"assert,omitempty"`
}

// Base lets a *StepBase (and thus every embedding step) satisfy the shared part of Step.
func (b *StepBase) Base() *StepBase { return b }

// NotifyEnabled reports whether the recipe author included a notify block (including notify: {}).
func (b *StepBase) NotifyEnabled() bool { return b.Notify != nil }

// cloned returns a deep copy of the base: maps and slices are copied so loop fan-out
// (which mutates Env/Host/Loop on the copy) cannot corrupt sibling steps.
func (b StepBase) cloned() StepBase {
	cp := b
	cp.Depends = slices.Clone(b.Depends)
	cp.Env = maps.Clone(b.Env)
	cp.Secrets = maps.Clone(b.Secrets)
	cp.EnvFrom = slices.Clone(b.EnvFrom)
	cp.NotifyHandler = slices.Clone(b.NotifyHandler)
	if len(b.Matrix) > 0 {
		cp.Matrix = make(map[string][]string, len(b.Matrix))
		for k, v := range b.Matrix {
			cp.Matrix[k] = slices.Clone(v)
		}
	}
	// Retry/LoopFrom/Notify/Hooks are pointers but loop fan-out never mutates them.
	return cp
}

const (
	minRecipeMaxParallel = 1
	maxRecipeMaxParallel = 128
)

// RemoteExec holds SSH / fan-out options for steps that target remote hosts.
type RemoteExec struct {
	SSHPort       int    `json:"ssh_port,omitempty" jsonschema:"default=22"`
	SSHPrivateKey string `json:"ssh_private_key,omitempty"`
	MaxParallel   int    `json:"max_parallel,omitempty" jsonschema:"default=0"`
	Serial        int    `json:"serial,omitempty"`
}

// EffectiveMaxParallel returns host-level parallelism for a step (SSH/SFTP batch).
// Step max_parallel overrides defaults; zero means caller should use its package default (32).
func (r *RemoteExec) EffectiveMaxParallel(defaults *RecipeDefaults) int {
	if r != nil && r.MaxParallel > 0 {
		return r.MaxParallel
	}
	if defaults != nil && defaults.MaxParallel > 0 {
		return defaults.MaxParallel
	}
	return 0
}

func validateMaxParallelField(where string, n int) error {
	if n == 0 {
		return nil
	}
	if n < minRecipeMaxParallel || n > maxRecipeMaxParallel {
		return fmt.Errorf("cuetry: %s.max_parallel must be between %d and %d", where, minRecipeMaxParallel, maxRecipeMaxParallel)
	}
	return nil
}

// Remote lets a *RemoteExec (and thus every embedding remote step) satisfy RemoteStep.
func (r *RemoteExec) Remote() *RemoteExec { return r }

// stepEntry is one registry record: how to construct the concrete step and which
// top-level JSON action keys identify it.
type stepEntry struct {
	kind       string
	actionKeys []string
	ctor       func() Step
}

// stepRegistry is an ordered list (deterministic iteration — Go randomizes maps).
// Populated by RegisterStep from step_concrete.go init(); this is the database/sql
// driver-registry idiom, the standard Go pattern for self-registering implementations.
var stepRegistry []stepEntry

// RegisterStep registers a concrete step kind, the JSON action keys that select it,
// and a constructor. Idempotent: re-registering a kind replaces the prior entry.
func RegisterStep(kind string, actionKeys []string, ctor func() Step) {
	for i := range stepRegistry {
		if stepRegistry[i].kind == kind {
			stepRegistry[i] = stepEntry{kind: kind, actionKeys: actionKeys, ctor: ctor}
			return
		}
	}
	stepRegistry = append(stepRegistry, stepEntry{kind: kind, actionKeys: actionKeys, ctor: ctor})
}

// StepKinds returns the registered kind identifiers in registration order.
func StepKinds() []string {
	out := make([]string, len(stepRegistry))
	for i := range stepRegistry {
		out[i] = stepRegistry[i].kind
	}
	return out
}

// reflectStepInstances returns one zero instance per registered kind (for schema gen).
func reflectStepInstances() []struct {
	Kind string
	Step Step
} {
	out := make([]struct {
		Kind string
		Step Step
	}, len(stepRegistry))
	for i := range stepRegistry {
		out[i] = struct {
			Kind string
			Step Step
		}{Kind: stepRegistry[i].kind, Step: stepRegistry[i].ctor()}
	}
	return out
}

// StepWrapper is the polymorphic JSON boundary: it decodes a raw step object into
// the correct concrete Step by inspecting which action key is present.
type StepWrapper struct {
	Step Step
}

// MarshalJSON emits the underlying concrete step (flattened base + action fields).
func (w StepWrapper) MarshalJSON() ([]byte, error) {
	if w.Step == nil {
		return []byte("null"), nil
	}
	return json.Marshal(w.Step)
}

// UnmarshalJSON inspects the raw object's top-level keys, finds the single matching
// action key in the registry, constructs the concrete step, and decodes into it.
func (w *StepWrapper) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	var matchedKind string
	var ctor func() Step
	for _, e := range stepRegistry {
		for _, key := range e.actionKeys {
			if _, ok := raw[key]; !ok {
				continue
			}
			// "command"/"render" are string keys: an empty value does not count.
			if v, ok := raw[key]; ok && (key == "command" || key == "render") {
				var s string
				if json.Unmarshal(v, &s) == nil && s == "" {
					continue
				}
			}
			if matchedKind != "" && matchedKind != e.kind {
				return fmt.Errorf("only one of command, render, put, get, script, agent_transfer, ai, template, plugin, tunnel, k8s, docker, opensearch, postgres, or recipe allowed")
			}
			matchedKind = e.kind
			ctor = e.ctor
		}
	}
	if ctor == nil {
		return fmt.Errorf("need exactly one of command, render, put, get, script, agent_transfer, ai, template, plugin, tunnel, k8s, docker, opensearch, postgres, or recipe")
	}

	step := ctor()
	if err := json.Unmarshal(data, step); err != nil {
		return err
	}
	w.Step = step
	return nil
}

// BuildStepJSONSchema reflects each registered concrete step kind into its own
// definition, so every kind exposes exactly its own fields (e.g. template/ai have no
// ssh_port / max_parallel). The result is consumed by the RecipeStudio frontend.
func BuildStepJSONSchema() map[string]any {
	reflector := jsonschema.Reflector{ExpandedStruct: true}
	definitions := make(map[string]any)

	for _, inst := range reflectStepInstances() {
		definitions[inst.Kind] = reflectToMap(&reflector, inst.Step)
	}
	definitions["defaults"] = reflectToMap(&reflector, &RecipeDefaults{})

	return map[string]any{
		"$schema":     "https://json-schema.org/draft/2020-12/schema",
		"type":        "object",
		"definitions": definitions,
	}
}

// reflectToMap reflects v into a JSON Schema and decodes it into a generic map.
// The schema is self-contained: nested types live under $defs and are referenced
// via $ref, which the frontend (recipeStudioUtils) resolves per kind.
func reflectToMap(reflector *jsonschema.Reflector, v any) map[string]any {
	schema := reflector.Reflect(v)
	b, _ := json.Marshal(schema)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}
