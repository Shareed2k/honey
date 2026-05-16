package cuetry

import (
	"fmt"
	"regexp"
	"strings"
)

// ExecutionMode is how recipe steps are ordered at run time.
type ExecutionMode int

const (
	// ExecutionModeLinear runs steps in array order (default).
	ExecutionModeLinear ExecutionMode = iota
	// ExecutionModeGraph runs steps by id/depends DAG with parallel waves.
	ExecutionModeGraph
)

var recipeStepIDPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)

// RecipeExecutionMode returns linear (default) or graph from recipe.type.
func RecipeExecutionMode(r Recipe) (ExecutionMode, error) {
	t := strings.TrimSpace(strings.ToLower(r.Type))
	switch t {
	case "", "linear":
		return ExecutionModeLinear, nil
	case "graph":
		return ExecutionModeGraph, nil
	default:
		return ExecutionModeLinear, fmt.Errorf("cuetry: recipe.type must be \"linear\" or \"graph\", got %q", r.Type)
	}
}

// StepGraph is a validated DAG over recipe steps (graph mode only).
type StepGraph struct {
	IDToIndex map[string]int
	IndexToID []string
	Depends   [][]int // step index -> dependency indices
	Children  [][]int // reverse edges
	TopoOrder []int
	Waves     [][]int
	AIIndex   int // >=0 when recipe has an ai step
}

// BuildStepGraph validates ids and depends, detects cycles, and computes topo order and waves.
func BuildStepGraph(steps []RecipeStep) (*StepGraph, error) {
	n := len(steps)
	if n == 0 {
		return nil, fmt.Errorf("cuetry: recipe has no steps")
	}
	sg := &StepGraph{
		IDToIndex: make(map[string]int, n),
		IndexToID: make([]string, n),
		Depends:   make([][]int, n),
		Children:  make([][]int, n),
		AIIndex:   -1,
	}
	for i, s := range steps {
		id := strings.TrimSpace(s.ID)
		if id == "" {
			return nil, fmt.Errorf("cuetry: steps[%d].id is required in graph mode", i)
		}
		if !recipeStepIDPattern.MatchString(id) {
			return nil, fmt.Errorf("cuetry: steps[%d].id %q must match [a-zA-Z][a-zA-Z0-9_-]*", i, id)
		}
		if _, dup := sg.IDToIndex[id]; dup {
			return nil, fmt.Errorf("cuetry: duplicate step id %q", id)
		}
		sg.IDToIndex[id] = i
		sg.IndexToID[i] = id
		kind, err := ClassifyStep(s)
		if err != nil {
			return nil, fmt.Errorf("cuetry: steps[%d]: %w", i, err)
		}
		if kind == StepKindAI {
			if sg.AIIndex >= 0 {
				return nil, fmt.Errorf("cuetry: recipe has more than one ai step")
			}
			sg.AIIndex = i
		}
	}
	nonAI := 0
	for i, s := range steps {
		for _, dep := range s.Depends {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				return nil, fmt.Errorf("cuetry: steps[%d].depends contains empty id", i)
			}
			j, ok := sg.IDToIndex[dep]
			if !ok {
				return nil, fmt.Errorf("cuetry: steps[%d].depends references unknown step id %q", i, dep)
			}
			if j == i {
				return nil, fmt.Errorf("cuetry: steps[%d].depends must not reference itself", i)
			}
			sg.Depends[i] = append(sg.Depends[i], j)
			sg.Children[j] = append(sg.Children[j], i)
		}
		kind, _ := ClassifyStep(s)
		if kind != StepKindAI {
			nonAI++
		}
	}
	if nonAI == 0 {
		return nil, fmt.Errorf("cuetry: graph recipe requires at least one non-ai step")
	}
	if sg.AIIndex >= 0 {
		for i, s := range steps {
			for _, dep := range s.Depends {
				dep = strings.TrimSpace(dep)
				if sg.IDToIndex[dep] == sg.AIIndex {
					return nil, fmt.Errorf("cuetry: steps[%d].id %q must not depend on ai step %q", i, sg.IndexToID[i], sg.IndexToID[sg.AIIndex])
				}
			}
		}
	}
	order, err := topoSort(sg.Depends, n)
	if err != nil {
		return nil, err
	}
	sg.TopoOrder = order
	sg.Waves = computeWaves(sg.Depends, n)
	return sg, nil
}

func topoSort(deps [][]int, n int) ([]int, error) {
	indeg := make([]int, n)
	for i := 0; i < n; i++ {
		indeg[i] = len(deps[i])
	}
	var q []int
	for i := 0; i < n; i++ {
		if indeg[i] == 0 {
			q = append(q, i)
		}
	}
	order := make([]int, 0, n)
	for len(q) > 0 {
		u := q[0]
		q = q[1:]
		order = append(order, u)
		for v := 0; v < n; v++ {
			for _, d := range deps[v] {
				if d == u {
					indeg[v]--
					if indeg[v] == 0 {
						q = append(q, v)
					}
					break
				}
			}
		}
	}
	if len(order) != n {
		return nil, fmt.Errorf("cuetry: recipe step graph contains a cycle")
	}
	return order, nil
}

func computeWaves(deps [][]int, n int) [][]int {
	order, err := topoSort(deps, n)
	if err != nil {
		return nil
	}
	level := make([]int, n)
	maxLvl := 0
	for _, u := range order {
		l := 0
		for _, d := range deps[u] {
			if level[d]+1 > l {
				l = level[d] + 1
			}
		}
		level[u] = l
		if l > maxLvl {
			maxLvl = l
		}
	}
	waves := make([][]int, maxLvl+1)
	for i, l := range level {
		waves[l] = append(waves[l], i)
	}
	return waves
}

// ValidateRecipeGraph checks graph/linear rules for ids, depends, ai, and kv_tunnel.
func ValidateRecipeGraph(r Recipe) error {
	mode, err := RecipeExecutionMode(r)
	if err != nil {
		return err
	}
	switch mode {
	case ExecutionModeLinear:
		for i, s := range r.Steps {
			hasWhen := strings.TrimSpace(s.When) != ""
			hasID := strings.TrimSpace(s.ID) != ""
			if hasID && !hasWhen {
				return fmt.Errorf("cuetry: steps[%d].id in linear mode is only allowed when when is set", i)
			}
			if hasWhen && !hasID {
				return fmt.Errorf("cuetry: steps[%d].when requires a non-empty id", i)
			}
			if len(s.Depends) > 0 {
				return fmt.Errorf("cuetry: steps[%d].depends is only allowed when recipe.type is \"graph\"", i)
			}
			if len(s.EnvFrom) > 0 {
				return fmt.Errorf("cuetry: steps[%d].env_from is only allowed when recipe.type is \"graph\"", i)
			}
		}
		return nil
	case ExecutionModeGraph:
		sg, err := BuildStepGraph(r.Steps)
		if err != nil {
			return err
		}
		for i, s := range r.Steps {
			kind, kerr := ClassifyStep(s)
			if kerr != nil {
				return fmt.Errorf("cuetry: steps[%d]: %w", i, kerr)
			}
			if len(s.EnvFrom) > 0 {
				if err := validateEnvFromRefs(i, s, sg); err != nil {
					return err
				}
				if kind != StepKindCommand && kind != StepKindScript && kind != StepKindPlugin {
					return fmt.Errorf("cuetry: steps[%d]: env_from is only supported for command, script, and plugin steps", i)
				}
			}
			if KVTunnelEnabled(s, r.Defaults) && strings.TrimSpace(s.ID) == "" {
				return fmt.Errorf("cuetry: steps[%d]: kv_tunnel in graph mode requires a non-empty id", i)
			}
			if err := validateStepWhen(i, ExecutionModeGraph, s, sg); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("cuetry: unknown execution mode")
	}
}

// FormatGraphWavesText returns a human-readable wave plan for graph recipes.
func FormatGraphWavesText(r Recipe) (string, error) {
	waves, err := GraphStepWaves(r)
	if err != nil {
		return "", err
	}
	sg, err := BuildStepGraph(r.Steps)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "recipe graph: %d waves (type=graph)\n", len(waves))
	for wi, wave := range waves {
		ids := make([]string, 0, len(wave))
		for _, idx := range wave {
			ids = append(ids, sg.IndexToID[idx])
		}
		par := ""
		if len(ids) > 1 {
			par = " (parallel)"
		}
		fmt.Fprintf(&b, "  wave %d%s: %s\n", wi+1, par, strings.Join(ids, ", "))
	}
	return b.String(), nil
}

// GraphStepWaves returns execution waves for a validated graph recipe.
func GraphStepWaves(r Recipe) ([][]int, error) {
	if _, err := RecipeExecutionMode(r); err != nil {
		return nil, err
	}
	sg, err := BuildStepGraph(r.Steps)
	if err != nil {
		return nil, err
	}
	return sg.Waves, nil
}

// BuildStepGraphFromRecipe builds the step graph when mode is graph.
func BuildStepGraphFromRecipe(r Recipe) (*StepGraph, error) {
	mode, err := RecipeExecutionMode(r)
	if err != nil {
		return nil, err
	}
	if mode != ExecutionModeGraph {
		return nil, fmt.Errorf("cuetry: not a graph recipe")
	}
	return BuildStepGraph(r.Steps)
}

// AncestorHistoryOrder returns succeeded step indices in topological order for ai transcript.
func (sg *StepGraph) AncestorHistoryOrder(aiIndex int, succeeded map[int]bool) []int {
	if aiIndex < 0 || aiIndex >= len(sg.TopoOrder) {
		return nil
	}
	need := make(map[int]bool)
	var mark func(u int)
	mark = func(u int) {
		if need[u] {
			return
		}
		need[u] = true
		for _, d := range sg.Depends[u] {
			mark(d)
		}
	}
	mark(aiIndex)
	var out []int
	for _, idx := range sg.TopoOrder {
		if idx == aiIndex {
			continue
		}
		if need[idx] && succeeded[idx] {
			out = append(out, idx)
		}
	}
	return out
}

// StepRunState is the scheduler state for one step in graph mode.
type StepRunState int

const (
	// StepRunPending means dependencies are not yet satisfied.
	StepRunPending StepRunState = iota
	// StepRunReady means the step may be scheduled.
	StepRunReady
	// StepRunRunning means the step is executing.
	StepRunRunning
	// StepRunSucceeded means the step completed without fatal failure.
	StepRunSucceeded
	// StepRunFailed means the step failed or all hosts had transient SSH errors.
	StepRunFailed
	// StepRunSkipped means a dependency failed and this step was not run.
	StepRunSkipped
)

// MarkSkippedDescendants marks all transitive children of from as skipped in state.
func (sg *StepGraph) MarkSkippedDescendants(from int, state []StepRunState) {
	for _, c := range sg.Children[from] {
		if state[c] == StepRunPending || state[c] == StepRunReady {
			state[c] = StepRunSkipped
			sg.MarkSkippedDescendants(c, state)
		}
	}
}
