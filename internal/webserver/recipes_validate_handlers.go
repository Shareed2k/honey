package webserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/safepath"
)

type validateContentRequest struct {
	RecipeContent *cuetry.Recipe `json:"recipe_content"`
}

type validateContentError struct {
	Path    string `json:"path,omitempty"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// ResolvedStepSummary is the per-step shape returned to the WebUI's Plan view.
// It mirrors cuetry.StepSummary; keep it small and JSON-stable — the wizard
// renders it directly.
type ResolvedStepSummary struct {
	Index   int      `json:"index"`
	ID      string   `json:"id,omitempty"`
	Depends []string `json:"depends,omitempty"`
	Wave    int      `json:"wave,omitempty"`
	Kind    string   `json:"kind"`
	Host    string   `json:"host"`
	RunAs   string   `json:"run_as,omitempty"`
	Preview string   `json:"preview"`
}

type validateContentResponse struct {
	Plan   string                  `json:"plan,omitempty"`
	Steps  []ResolvedStepSummary   `json:"steps,omitempty"`
	Graph  *cuetry.RecipeGraphPlan `json:"graph,omitempty"`
	Errors []validateContentError  `json:"errors,omitempty"`
}

type graphPlanRequest struct {
	Path          string         `json:"path"`
	RecipeContent *cuetry.Recipe `json:"recipe_content"`
}

// handleRecipesValidateContent validates in-editor recipe JSON and returns plan/steps or errors.
// @Summary Validate recipe JSON
// @Tags recipes
// @Accept json
// @Produce json
// @Param body body object true "recipe_content object"
// @Success 200 {object} object "plan and steps on success"
// @Failure 400 {object} object "errors array in validateContentResponse shape"
// @Router /api/v1/recipes/validate-content [post]
// @Security BearerAuth
func (*Server) handleRecipesValidateContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body validateContentRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeValidationErrors(w, []validateContentError{{Kind: "json", Message: err.Error()}})
		return
	}
	if body.RecipeContent == nil {
		writeValidationErrors(w, []validateContentError{{Kind: "schema", Message: "recipe_content required"}})
		return
	}
	if err := cuetry.ValidateParsedRecipe(*body.RecipeContent, nil); err != nil {
		writeValidationErrors(w, []validateContentError{{Kind: "validation", Message: err.Error()}})
		return
	}
	plan, summaries, err := cuetry.RenderDryRunPlan(*body.RecipeContent)
	if err != nil {
		writeValidationErrors(w, []validateContentError{{Kind: "resolve", Message: err.Error()}})
		return
	}
	steps := make([]ResolvedStepSummary, len(summaries))
	for i, s := range summaries {
		steps[i] = ResolvedStepSummary{
			Index:   s.Index,
			ID:      s.ID,
			Depends: s.Depends,
			Wave:    s.Wave,
			Kind:    s.Kind,
			Host:    s.Host,
			RunAs:   s.RunAs,
			Preview: s.Preview,
		}
	}
	resp := validateContentResponse{Plan: plan, Steps: steps}
	if mode, merr := cuetry.RecipeExecutionMode(*body.RecipeContent); merr == nil && mode == cuetry.ExecutionModeGraph {
		if gp, gerr := cuetry.BuildRecipeGraphPlan(*body.RecipeContent); gerr == nil {
			resp.Graph = gp
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleRecipesGraphPlan returns a structured DAG plan for graph recipes.
// @Summary Build graph recipe plan
// @Tags recipes
// @Accept json
// @Produce json
// @Param body body object true "path or recipe_content"
// @Success 200 {object} cuetry.RecipeGraphPlan
// @Failure 400 {object} map[string]string
// @Router /api/v1/recipes/graph-plan [post]
// @Security BearerAuth
func (*Server) handleRecipesGraphPlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body graphPlanRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}
	var recipe cuetry.Recipe
	switch {
	case body.RecipeContent != nil:
		recipe = *body.RecipeContent
	case strings.TrimSpace(body.Path) != "":
		cp, err := normalizeRecipePath(body.Path)
		if err != nil {
			httpError(w, err, http.StatusBadRequest)
			return
		}
		if _, ok := allowedRecipePathSet()[cp]; !ok {
			httpError(w, fmt.Errorf("recipe path not allowed"), http.StatusBadRequest)
			return
		}
		raw, err := safepath.ReadFile(cp)
		if err != nil {
			httpError(w, fmt.Errorf("read recipe: %w", err), http.StatusBadRequest)
			return
		}
		parsed, err := cuetry.ParseRemoteRecipe(raw, nil)
		if err != nil {
			httpError(w, fmt.Errorf("parse recipe: %w", err), http.StatusBadRequest)
			return
		}
		recipe = parsed
	default:
		httpError(w, fmt.Errorf("path or recipe_content required"), http.StatusBadRequest)
		return
	}
	if err := cuetry.ValidateParsedRecipe(recipe, nil); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	plan, err := cuetry.BuildRecipeGraphPlan(recipe)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(plan)
}

func writeValidationErrors(w http.ResponseWriter, errs []validateContentError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(validateContentResponse{Errors: errs})
}

type recipesParseRequest struct {
	Path string `json:"path"`
}

// handleRecipesParse reads and parses a recipe file from an allowed path.
// @Summary Parse recipe file to JSON
// @Tags recipes
// @Accept json
// @Produce json
// @Param body body object true "path field: absolute recipe file"
// @Success 200 {object} map[string]interface{} "recipe object"
// @Failure 400 {object} map[string]string
// @Router /api/v1/recipes/parse [post]
// @Security BearerAuth
func (*Server) handleRecipesParse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body recipesParseRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}
	cp, err := normalizeRecipePath(body.Path)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if _, ok := allowedRecipePathSet()[cp]; !ok {
		httpError(w, fmt.Errorf("recipe path not allowed"), http.StatusBadRequest)
		return
	}
	raw, err := safepath.ReadFile(cp)
	if err != nil {
		httpError(w, fmt.Errorf("read recipe: %w", err), http.StatusBadRequest)
		return
	}
	recipe, err := cuetry.ParseRemoteRecipe(raw, nil)
	if err != nil {
		httpError(w, fmt.Errorf("parse recipe: %w", err), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"recipe": recipe})
}
