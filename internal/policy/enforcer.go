// Package policy evaluates Open Policy Agent (OPA) rego policies for honey's
// authorization decisions: recipe admission, API gating, in-recipe checks, and
// host-list filtering. A nil *Enforcer is a no-op allow everywhere it is used,
// so OPA stays opt-in and the zero configuration is backward-compatible.
package policy

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/open-policy-agent/opa/v1/rego"
)

//go:embed policies/*.rego
var embeddedPolicies embed.FS

// Decision is the result of evaluating the honey policy package against an input.
type Decision struct {
	Allow      bool
	DenyReason string
}

// Enforcer holds a prepared rego query over the data.honey package. It is safe
// for concurrent use: PreparedEvalQuery.Eval does not mutate shared state.
type Enforcer struct {
	query rego.PreparedEvalQuery
}

// New builds an Enforcer from .rego modules. When policyDir is non-empty its
// *.rego files are loaded; otherwise the embedded default policy is used.
func New(ctx context.Context, policyDir string) (*Enforcer, error) {
	modules, err := loadModules(policyDir)
	if err != nil {
		return nil, err
	}
	return prepareEnforcer(ctx, modules)
}

// NewFromSource compiles a single named .rego source into an Enforcer. Used by
// the in-recipe opa step, which loads a policy file relative to the recipe.
func NewFromSource(ctx context.Context, name, src string) (*Enforcer, error) {
	return prepareEnforcer(ctx, map[string]string{name: src})
}

// Evaluate runs the policy against input and returns the Decision. A query that
// yields no result set (e.g. an undefined document) is treated as deny.
func (e *Enforcer) Evaluate(ctx context.Context, input map[string]any) (Decision, error) {
	rs, err := e.query.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		return Decision{Allow: false}, fmt.Errorf("policy: eval: %w", err)
	}
	if len(rs) == 0 || len(rs[0].Expressions) == 0 {
		return Decision{Allow: false}, nil
	}
	obj, _ := rs[0].Expressions[0].Value.(map[string]any)
	allow, _ := obj["allow"].(bool)
	reason, _ := obj["deny_reason"].(string)
	return Decision{Allow: allow, DenyReason: reason}, nil
}

func prepareEnforcer(ctx context.Context, modules map[string]string) (*Enforcer, error) {
	opts := make([]func(*rego.Rego), 0, len(modules)+1)
	opts = append(opts, rego.Query("data.honey"))
	for path, src := range modules {
		opts = append(opts, rego.Module(path, src))
	}
	q, err := rego.New(opts...).PrepareForEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("policy: prepare: %w", err)
	}
	return &Enforcer{query: q}, nil
}

func loadModules(policyDir string) (map[string]string, error) {
	if policyDir != "" {
		return loadDir(policyDir)
	}
	return loadEmbedded()
}

func loadEmbedded() (map[string]string, error) {
	entries, err := embeddedPolicies.ReadDir("policies")
	if err != nil {
		return nil, fmt.Errorf("policy: read embedded: %w", err)
	}
	m := make(map[string]string, len(entries))
	for _, e := range entries {
		b, err := embeddedPolicies.ReadFile("policies/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("policy: read embedded %q: %w", e.Name(), err)
		}
		m[e.Name()] = string(b)
	}
	return m, nil
}

func loadDir(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("policy: read dir %q: %w", dir, err)
	}
	m := make(map[string]string, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".rego" {
			continue
		}
		// #nosec G304 -- dir is operator-set config (HONEY_POLICY_DIR); e.Name()
		// is a single DirEntry component from ReadDir, so no traversal is possible.
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("policy: read %q: %w", e.Name(), err)
		}
		m[e.Name()] = string(b)
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("policy: no .rego files in %q", dir)
	}
	return m, nil
}
