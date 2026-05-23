package cuetry

import (
	"fmt"
	"strings"
)

// GraphPlanNode is one step in a recipe graph plan (API / viewer).
type GraphPlanNode struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Host     string `json:"host"`
	Wave     int    `json:"wave,omitempty"`
	When     string `json:"when,omitempty"`
	Retry    string `json:"retry,omitempty"`
	Notify   bool   `json:"notify,omitempty"`
	KVTunnel bool   `json:"kv_tunnel,omitempty"`
	Preview  string `json:"preview,omitempty"`
}

// GraphPlanEdge is a depends edge between step ids.
type GraphPlanEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// RecipeGraphPlan is a structured DAG view of a graph recipe.
type RecipeGraphPlan struct {
	Type    string            `json:"type"`
	Waves   [][]GraphPlanNode `json:"waves,omitempty"`
	Nodes   []GraphPlanNode   `json:"nodes"`
	Edges   []GraphPlanEdge   `json:"edges"`
	Mermaid string            `json:"mermaid,omitempty"`
}

// BuildRecipeGraphPlan builds a graph plan from a validated graph recipe.
func BuildRecipeGraphPlan(r Recipe) (*RecipeGraphPlan, error) {
	mode, err := RecipeExecutionMode(r)
	if err != nil {
		return nil, err
	}
	if mode != ExecutionModeGraph {
		return nil, fmt.Errorf("cuetry: recipe.type must be \"graph\"")
	}
	sg, err := BuildStepGraph(r.Steps)
	if err != nil {
		return nil, err
	}
	waveOf := make(map[int]int, len(r.Steps))
	for w, wave := range sg.Waves {
		for _, idx := range wave {
			waveOf[idx] = w + 1
		}
	}
	plan := &RecipeGraphPlan{
		Type:  "graph",
		Nodes: make([]GraphPlanNode, 0, len(r.Steps)),
	}
	for i, step := range r.Steps {
		kind, kerr := ClassifyStep(step)
		if kerr != nil {
			return nil, fmt.Errorf("step %d: %w", i, kerr)
		}
		n := GraphPlanNode{
			Index:    i,
			ID:       sg.IndexToID[i],
			Kind:     StepKindLabel(kind),
			Host:     strings.TrimSpace(step.Host),
			Wave:     waveOf[i],
			When:     strings.TrimSpace(step.When),
			Retry:    retrySummary(step, r.Defaults),
			Notify:   step.NotifyEnabled(),
			KVTunnel: KVTunnelEnabled(step, r.Defaults),
			Preview:  previewForStep(kind, step),
		}
		plan.Nodes = append(plan.Nodes, n)
	}
	for i, deps := range sg.Depends {
		to := sg.IndexToID[i]
		for _, d := range deps {
			plan.Edges = append(plan.Edges, GraphPlanEdge{From: sg.IndexToID[d], To: to})
		}
	}
	plan.Waves = make([][]GraphPlanNode, len(sg.Waves))
	for w, wave := range sg.Waves {
		for _, idx := range wave {
			for _, n := range plan.Nodes {
				if n.Index == idx {
					plan.Waves[w] = append(plan.Waves[w], n)
					break
				}
			}
		}
	}
	plan.Mermaid = formatGraphMermaid(sg, r)
	return plan, nil
}

func formatGraphMermaid(sg *StepGraph, r Recipe) string {
	var b strings.Builder
	b.WriteString("flowchart TD\n")
	for i, step := range r.Steps {
		id := sg.IndexToID[i]
		kind, _ := ClassifyStep(step)
		label := fmt.Sprintf("%s\\n%s", id, StepKindLabel(kind))
		fmt.Fprintf(&b, "  %s[%q]\n", mermaidNodeID(id), label)
	}
	for i, deps := range sg.Depends {
		to := mermaidNodeID(sg.IndexToID[i])
		for _, d := range deps {
			from := mermaidNodeID(sg.IndexToID[d])
			fmt.Fprintf(&b, "  %s --> %s\n", from, to)
		}
	}
	return b.String()
}

func mermaidNodeID(id string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, id)
}
