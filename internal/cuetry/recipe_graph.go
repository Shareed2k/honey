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
	// ExecutionModeController exposes each step as a tool and lets an LLM decide
	// which to run, in what order, until the recipe's tasks (goals) are satisfied.
	ExecutionModeController
)

var recipeStepIDPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_\-\[\]=,]*$`)

// RecipeExecutionMode returns linear (default) or graph from recipe.type.
func RecipeExecutionMode(r Recipe) (ExecutionMode, error) {
	t := strings.TrimSpace(strings.ToLower(r.Type))
	switch t {
	case "", "linear":
		return ExecutionModeLinear, nil
	case "graph":
		return ExecutionModeGraph, nil
	case "controller":
		return ExecutionModeController, nil
	default:
		return ExecutionModeLinear, fmt.Errorf("cuetry: recipe.type must be \"linear\", \"graph\", or \"controller\", got %q", r.Type)
	}
}

// StepGraph is a validated DAG over recipe steps (graph mode only).
type StepGraph struct {
	IDToIndex      map[string]int
	IndexToID      []string
	Depends        [][]int // step index -> dependency indices
	Children       [][]int // reverse edges
	TopoOrder      []int
	Waves          [][]int
	SummarizeIndex int // >=0 when recipe has a summarize step
}

// BuildStepGraph validates ids and depends, detects cycles, and computes topo order and waves.
func BuildStepGraph(steps []StepWrapper) (*StepGraph, error) {
	n := len(steps)
	if n == 0 {
		return nil, fmt.Errorf("cuetry: recipe has no steps")
	}
	sg := &StepGraph{
		IDToIndex:      make(map[string]int, n),
		IndexToID:      make([]string, n),
		Depends:        make([][]int, n),
		Children:       make([][]int, n),
		SummarizeIndex: -1,
	}
	for i, w := range steps {
		b := w.Step.Base()
		id := strings.TrimSpace(b.ID)
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
		if w.Step.Kind() == KindSummarize {
			if sg.SummarizeIndex >= 0 {
				return nil, fmt.Errorf("cuetry: recipe has more than one summarize step")
			}
			sg.SummarizeIndex = i
		}
	}
	nonSummarize := 0
	for i, w := range steps {
		for _, dep := range w.Step.Base().Depends {
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
		if w.Step.Kind() != KindSummarize {
			nonSummarize++
		}
	}
	if nonSummarize == 0 {
		return nil, fmt.Errorf("cuetry: graph recipe requires at least one non-summarize step")
	}

	// Validate rescue references
	for i, w := range steps {
		for _, res := range w.Step.Base().Rescue {
			res = strings.TrimSpace(res)
			if res == "" {
				return nil, fmt.Errorf("cuetry: steps[%d].rescue contains empty id", i)
			}
			j, ok := sg.IDToIndex[res]
			if !ok {
				return nil, fmt.Errorf("cuetry: steps[%d].rescue references unknown step id %q", i, res)
			}
			if j == i {
				return nil, fmt.Errorf("cuetry: steps[%d].rescue must not reference itself", i)
			}

			// Implicitly make the rescue step depend on the failing step
			// so it doesn't execute before it.
			alreadyDepends := false
			for _, d := range steps[j].Step.Base().Depends {
				if d == sg.IndexToID[i] {
					alreadyDepends = true
					break
				}
			}
			if !alreadyDepends {
				sg.Depends[j] = append(sg.Depends[j], i)
				sg.Children[i] = append(sg.Children[i], j)
			}

			// Note: At execution time, the engine must check if j was triggered by a rescue.
		}
	}

	if err := applyInterceptSessionStepEdges(sg, steps); err != nil {
		return nil, err
	}

	if sg.SummarizeIndex >= 0 {
		for i, w := range steps {
			for _, dep := range w.Step.Base().Depends {
				dep = strings.TrimSpace(dep)
				if sg.IDToIndex[dep] == sg.SummarizeIndex {
					return nil, fmt.Errorf("cuetry: steps[%d].id %q must not depend on summarize step %q", i, sg.IndexToID[i], sg.IndexToID[sg.SummarizeIndex])
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

// applyInterceptSessionStepEdges validates intercept session_step references and
// derives the implicit dependency: a step reusing another intercept step's
// session must run after the step that established it, without the author
// writing an explicit depends. Mirrors the rescue-edge derivation in
// BuildStepGraph.
func applyInterceptSessionStepEdges(sg *StepGraph, steps []StepWrapper) error {
	for i, w := range steps {
		is, ok := w.Step.(*InterceptStep)
		if !ok || is.Intercept == nil || is.Intercept.SessionStep == "" {
			continue
		}
		sessionStep := strings.TrimSpace(is.Intercept.SessionStep)
		j, ok := sg.IDToIndex[sessionStep]
		if !ok {
			return fmt.Errorf("cuetry: steps[%d].intercept.session_step references unknown step id %q", i, sessionStep)
		}
		if j == i {
			return fmt.Errorf("cuetry: steps[%d].intercept.session_step must not reference itself", i)
		}
		alreadyDepends := false
		for _, d := range sg.Depends[i] {
			if d == j {
				alreadyDepends = true
				break
			}
		}
		if !alreadyDepends {
			sg.Depends[i] = append(sg.Depends[i], j)
			sg.Children[j] = append(sg.Children[j], i)
		}
	}
	return nil
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
		for i, ws := range r.Steps {
			b := ws.Step.Base()
			hasWhen := strings.TrimSpace(b.When) != ""
			hasID := strings.TrimSpace(b.ID) != ""
			if hasID && !hasWhen {
				return fmt.Errorf("cuetry: steps[%d].id in linear mode is only allowed when when is set", i)
			}
			if hasWhen && !hasID {
				return fmt.Errorf("cuetry: steps[%d].when requires a non-empty id", i)
			}
			if len(b.Depends) > 0 {
				return fmt.Errorf("cuetry: steps[%d].depends is only allowed when recipe.type is \"graph\"", i)
			}
			if len(b.EnvFrom) > 0 {
				return fmt.Errorf("cuetry: steps[%d].env_from is only allowed when recipe.type is \"graph\"", i)
			}
			if b.TriggerRule != "" && b.TriggerRule != "all_success" {
				return fmt.Errorf("cuetry: steps[%d].trigger_rule is only allowed when recipe.type is \"graph\"", i)
			}
		}
		return nil
	case ExecutionModeGraph:
		// Expand matrices before graph validation
		if err := ExpandMatrixSteps(&r); err != nil {
			return err
		}
		if err := validateUniqueTemplateOutputs(r.Steps); err != nil {
			return err
		}
		outputByName := templateOutputProducers(r.Steps)
		sg, err := BuildStepGraph(r.Steps)
		if err != nil {
			return err
		}
		for i, ws := range r.Steps {
			b := ws.Step.Base()
			if len(b.EnvFrom) > 0 {
				if err := validateEnvFromRefs(i, b, sg, outputByName, r.MatrixExpansions); err != nil {
					return err
				}
			}
			if KVTunnelEnabled(ws.Step, r.Defaults) && strings.TrimSpace(b.ID) == "" {
				return fmt.Errorf("cuetry: steps[%d]: kv_tunnel in graph mode requires a non-empty id", i)
			}
			if err := validateStepWhen(i, ExecutionModeGraph, b, sg); err != nil {
				return err
			}
			switch b.TriggerRule {
			case "", "all_success", "one_failed", "all_done":
				// valid
			default:
				return fmt.Errorf("cuetry: steps[%d] invalid trigger_rule %q", i, b.TriggerRule)
			}
		}
		return nil
	case ExecutionModeController:
		return validateControllerRecipe(r)
	default:
		return fmt.Errorf("cuetry: unknown execution mode")
	}
}

// validateControllerRecipe checks the rules for type: "controller": at least one
// task (goal), and every step has a unique non-empty id (the LLM tool handle).
// depends/env_from/trigger_rule/rescue are meaningless when the LLM orders steps,
// so they are rejected.
func validateControllerRecipe(r Recipe) error {
	if len(r.Tasks) == 0 {
		return fmt.Errorf("cuetry: controller mode requires at least one task")
	}
	for i, t := range r.Tasks {
		if strings.TrimSpace(t.Name) == "" {
			return fmt.Errorf("cuetry: tasks[%d].name is required", i)
		}
		if strings.TrimSpace(t.Description) == "" {
			return fmt.Errorf("cuetry: tasks[%d].description is required", i)
		}
	}
	seen := make(map[string]int, len(r.Steps))
	for i, ws := range r.Steps {
		b := ws.Step.Base()
		id := strings.TrimSpace(b.ID)
		if id == "" {
			return fmt.Errorf("cuetry: steps[%d].id is required in controller mode (it is the LLM tool handle)", i)
		}
		if !recipeStepIDPattern.MatchString(id) {
			return fmt.Errorf("cuetry: steps[%d].id %q must match [a-zA-Z][a-zA-Z0-9_-]*", i, id)
		}
		if prev, dup := seen[id]; dup {
			return fmt.Errorf("cuetry: duplicate step id %q (steps[%d] and steps[%d])", id, prev, i)
		}
		seen[id] = i
		if len(b.Depends) > 0 {
			return fmt.Errorf("cuetry: steps[%d].depends is not allowed in controller mode (the LLM decides order)", i)
		}
		if len(b.EnvFrom) > 0 {
			return fmt.Errorf("cuetry: steps[%d].env_from is not allowed in controller mode", i)
		}
		if b.TriggerRule != "" && b.TriggerRule != "all_success" {
			return fmt.Errorf("cuetry: steps[%d].trigger_rule is not allowed in controller mode", i)
		}
		if len(b.Rescue) > 0 {
			return fmt.Errorf("cuetry: steps[%d].rescue is not allowed in controller mode", i)
		}
		if is, ok := ws.Step.(*InterceptStep); ok && is.Intercept != nil && strings.TrimSpace(is.Intercept.SessionStep) != "" {
			return fmt.Errorf("cuetry: steps[%d].intercept.session_step is not allowed in controller mode", i)
		}
	}
	return nil
}

// FormatGraphWavesText returns a human-readable wave plan for graph recipes.
func FormatGraphWavesText(r Recipe) (string, error) {
	if err := ExpandMatrixSteps(&r); err != nil {
		return "", err
	}
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
func BuildStepGraphFromRecipe(r *Recipe) (*StepGraph, error) {
	mode, err := RecipeExecutionMode(*r)
	if err != nil {
		return nil, err
	}
	if mode != ExecutionModeGraph {
		return nil, fmt.Errorf("cuetry: not a graph recipe")
	}
	if err := ExpandMatrixSteps(r); err != nil {
		return nil, err
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
