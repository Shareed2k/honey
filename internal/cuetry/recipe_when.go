package cuetry

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/stepkv"
)

const maxWhenExprLen = 4 << 10

var whenStepIDInExpr = regexp.MustCompile(`steps\s*\[\s*['"]([a-zA-Z][a-zA-Z0-9_-]*)['"]\s*\]`)

// KVReader reads operator-local stepkv keys for CEL kv_get/kv_has.
type KVReader interface {
	Get(key string) (value string, found bool, err error)
}

type stepkvReader struct {
	sess *stepkv.Session
}

func (r stepkvReader) Get(key string) (string, bool, error) {
	if r.sess == nil {
		return "", false, nil
	}
	return r.sess.Get(key)
}

// WhenProgram is a compiled CEL when expression.
type WhenProgram struct {
	prog cel.Program
	expr string
}

// CompileWhen validates and compiles a when expression.
func CompileWhen(expr string) (*WhenProgram, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, fmt.Errorf("cuetry: when expression is empty")
	}
	if len(expr) > maxWhenExprLen {
		return nil, fmt.Errorf("cuetry: when expression exceeds %d bytes", maxWhenExprLen)
	}
	env, err := newWhenEnv()
	if err != nil {
		return nil, err
	}
	ast, iss := env.Compile(expr)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("cuetry: when: %w", iss.Err())
	}
	prg, err := env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("cuetry: when: %w", err)
	}
	return &WhenProgram{prog: prg, expr: expr}, nil
}

func newWhenEnv() (*cel.Env, error) {
	return cel.NewEnv(
		cel.Variable("host", cel.DynType),
		cel.Variable("dest", cel.DynType),
		cel.Variable("steps", cel.DynType),
		cel.Variable("secrets", cel.MapType(cel.StringType, cel.StringType)),
		cel.Variable("env", cel.MapType(cel.StringType, cel.StringType)),
		cel.Variable("execute", cel.BoolType),
		cel.Variable("recipe_name", cel.StringType),
		cel.Variable("facts", cel.MapType(cel.StringType, cel.DynType)),
		cel.Function("kv_get",
			cel.Overload("kv_get_string", []*cel.Type{cel.StringType}, cel.StringType,
				cel.FunctionBinding(func(args ...ref.Val) ref.Val {
					key, ok := args[0].(types.String)
					if !ok {
						return types.ValOrErr(args[0], "kv_get: expected string key")
					}
					return types.String(kvGetBinding(string(key)))
				}),
			),
		),
		cel.Function("kv_has",
			cel.Overload("kv_has_string", []*cel.Type{cel.StringType}, cel.BoolType,
				cel.FunctionBinding(func(args ...ref.Val) ref.Val {
					key, ok := args[0].(types.String)
					if !ok {
						return types.ValOrErr(args[0], "kv_has: expected string key")
					}
					return types.Bool(kvHasBinding(string(key)))
				}),
			),
		),
	)
}

var (
	kvBindMu     sync.Mutex
	kvGetBinding func(string) string
	kvHasBinding func(string) bool
)

// WhenEvalOpts carries per-evaluation context for CEL when.
type WhenEvalOpts struct {
	RecipeName string
	Execute    bool
	Host       hosts.Record
	Dest       *hosts.Record
	Steps      map[string]StepView
	Secrets    map[string]string
	Env        map[string]string
	KV         KVReader
	Facts      map[string]any
}

// DefaultFacts returns the default fallback facts map with unknown values.
func DefaultFacts() map[string]any {
	return map[string]any{
		"os":      "unknown",
		"arch":    "unknown",
		"id":      "unknown",
		"version": "unknown",
		"init":    "unknown",
		"pkg_mgr": "unknown",
	}
}

// EvalWhen evaluates a compiled when program; false means skip the host/step.
func EvalWhen(prog *WhenProgram, opts WhenEvalOpts) (bool, error) {
	if prog == nil || prog.prog == nil {
		return true, nil
	}
	kvBindMu.Lock()
	defer kvBindMu.Unlock()
	prevGet, prevHas := kvGetBinding, kvHasBinding
	defer func() {
		kvGetBinding, kvHasBinding = prevGet, prevHas
	}()
	kv := opts.KV
	if kv == nil {
		kv = stepkvReader{}
	}
	kvGetBinding = func(key string) string {
		key = strings.TrimSpace(key)
		if key == "" {
			return ""
		}
		if err := stepkvValidateKey(key); err != nil {
			return ""
		}
		v, found, err := kv.Get(key)
		if err != nil || !found {
			return ""
		}
		return v
	}
	kvHasBinding = func(key string) bool {
		key = strings.TrimSpace(key)
		if key == "" {
			return false
		}
		if err := stepkvValidateKey(key); err != nil {
			return false
		}
		_, found, err := kv.Get(key)
		return err == nil && found
	}

	facts := opts.Facts
	if facts == nil {
		facts = make(map[string]any)
	}

	act := map[string]any{
		"host":        hostToCELMap(opts.Host),
		"steps":       stepsToCELMap(opts.Steps),
		"secrets":     opts.Secrets,
		"env":         opts.Env,
		"execute":     opts.Execute,
		"recipe_name": opts.RecipeName,
		"facts":       facts,
	}
	if opts.Dest != nil {
		act["dest"] = hostToCELMap(*opts.Dest)
	} else {
		act["dest"] = map[string]any{}
	}

	out, _, err := prog.prog.Eval(act)
	if err != nil {
		return false, fmt.Errorf("cuetry: when eval: %w", err)
	}
	b, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("cuetry: when must evaluate to bool, got %T", out.Value())
	}
	return b, nil
}

func stepkvValidateKey(key string) error {
	if strings.TrimSpace(key) == "" || strings.Contains(key, "/") {
		return stepkv.ErrBadKey
	}
	if len(key) > 256 {
		return stepkv.ErrBadKey
	}
	return nil
}

func hostToCELMap(r hosts.Record) map[string]any {
	meta := make(map[string]string, len(r.Meta))
	for k, v := range r.Meta {
		meta[k] = v
	}
	extra := make([]string, len(r.ExtraIPs))
	copy(extra, r.ExtraIPs)
	return map[string]any{
		"name":      r.Name,
		"ip":        r.PrimaryIP,
		"provider":  r.Provider,
		"zone":      r.Zone,
		"region":    r.Region,
		"meta":      meta,
		"extra_ips": extra,
	}
}

func stepsToCELMap(steps map[string]StepView) map[string]any {
	out := make(map[string]any, len(steps))
	for id, v := range steps {
		out[id] = map[string]any{
			"succeeded": v.Succeeded,
			"skipped":   v.Skipped,
			"stdout":    v.Stdout,
			"exit_code": int64(v.ExitCode),
		}
	}
	return out
}

// stepIDsInWhenExpr extracts step ids referenced as steps['id'] in one when expression.
func stepIDsInWhenExpr(w string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, m := range whenStepIDInExpr.FindAllStringSubmatch(w, -1) {
		if len(m) > 1 && m[1] != "" {
			out[m[1]] = struct{}{}
		}
	}
	return out
}

// StepIDsReferencedByWhen returns step ids referenced as steps['id'] in when expressions.
func StepIDsReferencedByWhen(r Recipe) map[string]struct{} {
	out := make(map[string]struct{})
	for _, ws := range r.Steps {
		w := strings.TrimSpace(ws.Step.Base().When)
		if w == "" {
			continue
		}
		for id := range stepIDsInWhenExpr(w) {
			out[id] = struct{}{}
		}
	}
	return out
}

// RecipeUsesWhen reports whether any step has a when expression.
func RecipeUsesWhen(r Recipe) bool {
	for _, s := range r.Steps {
		if strings.TrimSpace(s.Step.Base().When) != "" {
			return true
		}
	}
	return false
}

// RecipeUsesKVInWhen reports whether any when expression calls kv_get or kv_has.
func RecipeUsesKVInWhen(r Recipe) bool {
	for _, s := range r.Steps {
		w := strings.TrimSpace(s.Step.Base().When)
		if w == "" {
			continue
		}
		if strings.Contains(w, "kv_get(") || strings.Contains(w, "kv_has(") {
			return true
		}
	}
	return false
}

func validateStepWhen(i int, mode ExecutionMode, step *StepBase, sg *StepGraph) error {
	w := strings.TrimSpace(step.When)
	if w == "" {
		return nil
	}
	id := strings.TrimSpace(step.ID)
	if id == "" {
		return fmt.Errorf("cuetry: steps[%d].when requires a non-empty id", i)
	}
	if _, err := CompileWhen(w); err != nil {
		return fmt.Errorf("cuetry: steps[%d].when: %w", i, err)
	}
	if mode == ExecutionModeGraph && sg != nil {
		depSet := make(map[string]struct{}, len(step.Depends))
		for _, d := range step.Depends {
			depSet[strings.TrimSpace(d)] = struct{}{}
		}
		for refID := range stepIDsInWhenExpr(w) {
			if _, ok := sg.IDToIndex[refID]; !ok {
				return fmt.Errorf("cuetry: steps[%d].when references unknown step id %q", i, refID)
			}
			if _, ok := depSet[refID]; !ok {
				return fmt.Errorf("cuetry: steps[%d].when references step %q which must appear in depends", i, refID)
			}
		}
	}
	return nil
}

// BuildSecretsMapForWhen merges defaults and step secret keys into a map for CEL (resolved or redacted).
func BuildSecretsMapForWhen(ctx context.Context, resolve bool, resolver SecretResolver, step *StepBase, defaults *RecipeDefaults) (map[string]string, error) {
	out := make(map[string]string)
	if defaults != nil && len(defaults.Secrets) > 0 {
		if err := MergeResolvedSecretsInto(ctx, resolve, resolver, out, defaults.Secrets, "defaults.secrets"); err != nil {
			return nil, err
		}
	}
	if len(step.Secrets) > 0 {
		if err := MergeResolvedSecretsInto(ctx, resolve, resolver, out, step.Secrets, "step.secrets"); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// BuildEnvMapForWhen merges recipe defaults/step env, CLI overrides, and host env for CEL when.
func BuildEnvMapForWhen(ctx context.Context, resolveSecrets bool, resolver SecretResolver, step *StepBase, defaults *RecipeDefaults, cliEnv map[string]string, host *hosts.Record) (map[string]string, error) {
	if host == nil {
		return nil, fmt.Errorf("cuetry: when env requires host")
	}
	return EffectiveEnvForRun(ctx, resolveSecrets, resolver, step, defaults, cliEnv, host)
}

// DeclaredSecretKeys returns union of secret keys from defaults and step.
func DeclaredSecretKeys(step *StepBase, defaults *RecipeDefaults) map[string]struct{} {
	out := make(map[string]struct{})
	if defaults != nil {
		for k := range defaults.Secrets {
			out[k] = struct{}{}
		}
	}
	for k := range step.Secrets {
		out[k] = struct{}{}
	}
	return out
}
