package webserver

import (
	"encoding/json"
	"fmt"

	"github.com/shareed2k/honey/internal/cuetry"
)

// MetaResponse is returned by GET /api/v1/meta.
type MetaResponse struct {
	Version                   string `json:"version"`
	Commit                    string `json:"commit"`
	Date                      string `json:"date"`
	ConfigPath                string `json:"config_path"`
	SessionRecordingAvailable bool   `json:"session_recording_available"`
	TerminalAssistAvailable   bool   `json:"terminal_assist_available"`
}

// ProvidersResponse is returned by GET /api/v1/providers.
type ProvidersResponse struct {
	Providers []string `json:"providers"`
}

// ConfigSchemaResponse is returned by GET /api/v1/config/schema.
type ConfigSchemaResponse struct {
	JSONSchema map[string]interface{} `json:"json_schema"`
	UISchema   any                    `json:"ui_schema"`
}

// StatusResponse is returned by config write endpoints.
type StatusResponse struct {
	Status string `json:"status"`
	Path   string `json:"path"`
}

// ConfigBackendEntryBody is one backends.{kind}[] element; shape depends on path param kind.
type ConfigBackendEntryBody map[string]interface{}

func recipeFromContentMap(m map[string]interface{}) (*cuetry.Recipe, error) {
	if m == nil {
		return nil, nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("recipe_content: %w", err)
	}
	var recipe cuetry.Recipe
	if err := json.Unmarshal(b, &recipe); err != nil {
		return nil, fmt.Errorf("recipe_content: %w", err)
	}
	return &recipe, nil
}

func graphPlanToResponse(gp *cuetry.RecipeGraphPlan) *RecipeGraphPlanResponse {
	if gp == nil {
		return nil
	}
	out := &RecipeGraphPlanResponse{
		Type:    gp.Type,
		Mermaid: gp.Mermaid,
		Nodes:   make([]GraphPlanNodeDoc, len(gp.Nodes)),
		Edges:   make([]GraphPlanEdgeDoc, len(gp.Edges)),
	}
	for i, n := range gp.Nodes {
		out.Nodes[i] = GraphPlanNodeDoc{
			Index: n.Index, ID: n.ID, Kind: n.Kind, Host: n.Host,
			Wave: n.Wave, When: n.When, KVTunnel: n.KVTunnel, Preview: n.Preview,
		}
	}
	for i, e := range gp.Edges {
		out.Edges[i] = GraphPlanEdgeDoc{From: e.From, To: e.To}
	}
	if len(gp.Waves) > 0 {
		out.Waves = make([][]GraphPlanNodeDoc, len(gp.Waves))
		for w, wave := range gp.Waves {
			out.Waves[w] = make([]GraphPlanNodeDoc, len(wave))
			for i, n := range wave {
				out.Waves[w][i] = GraphPlanNodeDoc{
					Index: n.Index, ID: n.ID, Kind: n.Kind, Host: n.Host,
					Wave: n.Wave, When: n.When, KVTunnel: n.KVTunnel, Preview: n.Preview,
				}
			}
		}
	}
	return out
}
