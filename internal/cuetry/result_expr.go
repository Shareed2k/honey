package cuetry

import (
	"fmt"
	"strings"

	"github.com/google/cel-go/cel"
)

// ResultExprProgram is a compiled changed_when / failed_when expression.
type ResultExprProgram struct {
	prog cel.Program
}

// ResultExprContext is the CEL-facing context for one step result.
type ResultExprContext struct {
	Stdout    string
	Stderr    string
	ExitCode  int
	Succeeded bool
	Changed   bool
	Host      map[string]any
	Facts     map[string]any
	Steps     map[string]StepView
	Outputs   map[string]any
	Item      string
}

// CompileResultBoolExpr validates and compiles a result override expression.
func CompileResultBoolExpr(expr string) (*ResultExprProgram, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, fmt.Errorf("cuetry: result expression is empty")
	}
	if len(expr) > maxWhenExprLen {
		return nil, fmt.Errorf("cuetry: result expression exceeds %d bytes", maxWhenExprLen)
	}
	env, err := cel.NewEnv(
		cel.Variable("stdout", cel.StringType),
		cel.Variable("output", cel.StringType),
		cel.Variable("stderr", cel.StringType),
		cel.Variable("exit_code", cel.IntType),
		cel.Variable("succeeded", cel.BoolType),
		cel.Variable("failed", cel.BoolType),
		cel.Variable("changed", cel.BoolType),
		cel.Variable("host", cel.DynType),
		cel.Variable("facts", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("steps", cel.DynType),
		cel.Variable("outputs", cel.DynType),
		cel.Variable("item", cel.StringType),
	)
	if err != nil {
		return nil, err
	}
	ast, iss := env.Compile(expr)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("cuetry: result expression: %w", iss.Err())
	}
	prg, err := env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("cuetry: result expression: %w", err)
	}
	return &ResultExprProgram{prog: prg}, nil
}

// EvalResultBoolExpr compiles and evaluates a result override expression.
func EvalResultBoolExpr(expr string, ctx ResultExprContext) (bool, error) {
	prog, err := CompileResultBoolExpr(expr)
	if err != nil {
		return false, err
	}
	return prog.Eval(ctx)
}

// Eval evaluates a compiled result expression.
func (p *ResultExprProgram) Eval(ctx ResultExprContext) (bool, error) {
	if p == nil {
		return false, fmt.Errorf("cuetry: nil result expression")
	}
	out, _, err := p.prog.Eval(map[string]any{
		"stdout":    ctx.Stdout,
		"output":    ctx.Stdout,
		"stderr":    ctx.Stderr,
		"exit_code": int64(ctx.ExitCode),
		"succeeded": ctx.Succeeded,
		"failed":    !ctx.Succeeded,
		"changed":   ctx.Changed,
		"host":      ctx.Host,
		"facts":     ctx.Facts,
		"steps":     stepsToCELMap(ctx.Steps),
		"outputs":   ctx.Outputs,
		"item":      ctx.Item,
	})
	if err != nil {
		return false, fmt.Errorf("cuetry: result expression eval: %w", err)
	}
	b, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("cuetry: result expression must evaluate to bool, got %T", out.Value())
	}
	return b, nil
}

func defaultRenderHosts(steps []RecipeStep) {
	for i := range steps {
		if strings.TrimSpace(steps[i].Render) != "" && strings.TrimSpace(steps[i].Host) == "" {
			steps[i].Host = MatchLocalAIHost
		}
	}
}
