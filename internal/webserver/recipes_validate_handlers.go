package webserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/safepath"
)

// ValidateContentRequest is the JSON body for POST /api/v1/recipes/validate-content.
type ValidateContentRequest struct {
	RecipeContent map[string]interface{} `json:"recipe_content"`
}

// ValidateContentError is one validation issue.
type ValidateContentError struct {
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
	When    string   `json:"when,omitempty"`
	Retry   string   `json:"retry,omitempty"`
	Notify  bool     `json:"notify,omitempty"`
	Preview string   `json:"preview"`
}

// ValidateContentResponse is returned on success or validation failure.
type ValidateContentResponse struct {
	Plan   string                  `json:"plan,omitempty"`
	Steps  []ResolvedStepSummary   `json:"steps,omitempty"`
	Graph  *cuetry.RecipeGraphPlan `json:"graph,omitempty"`
	Errors []ValidateContentError  `json:"errors,omitempty"`
}

// GraphPlanRequest is the JSON body for POST /api/v1/recipes/graph-plan.
type GraphPlanRequest struct {
	Path          string                 `json:"path,omitempty"`
	RecipeContent map[string]interface{} `json:"recipe_content,omitempty"`
}

// RecipesParseRequest is the JSON body for POST /api/v1/recipes/parse.
type RecipesParseRequest struct {
	Path string `json:"path"`
}

// RecipesParseResponse is the JSON body for a successful recipe parse.
type RecipesParseResponse struct {
	Recipe map[string]interface{} `json:"recipe"`
}

// handleRecipesValidateContent validates in-editor recipe JSON and returns plan/steps or errors.
// @Summary Validate recipe JSON
// @Tags recipes
// @Accept json
// @Produce json
// @Param body body ValidateContentRequest true "recipe object"
// @Success 200 {object} ValidateContentResponse
// @Failure 400 {object} ValidateContentResponse
// @Router /api/v1/recipes/validate-content [post]
// @Security BearerAuth
func (s *Server) handleRecipesValidateContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	start := time.Now()
	status := "ok"
	defer func() {
		if s.metrics != nil {
			s.metrics.ObserveRecipeValidate(status, time.Since(start))
		}
	}()
	var body ValidateContentRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		status = "error"
		writeValidationErrors(w, []ValidateContentError{{Kind: "json", Message: err.Error()}})
		return
	}
	recipe, err := recipeFromContentMap(body.RecipeContent)
	if err != nil {
		status = "error"
		writeValidationErrors(w, []ValidateContentError{{Kind: "json", Message: err.Error()}})
		return
	}
	if recipe == nil {
		status = "error"
		writeValidationErrors(w, []ValidateContentError{{Kind: "schema", Message: "recipe_content required"}})
		return
	}
	if err := cuetry.ValidateParsedRecipe(*recipe, nil); err != nil {
		status = "error"
		writeValidationErrors(w, []ValidateContentError{{Kind: "validation", Message: err.Error()}})
		return
	}
	plan, summaries, err := cuetry.RenderDryRunPlan(*recipe)
	if err != nil {
		status = "error"
		writeValidationErrors(w, []ValidateContentError{{Kind: "resolve", Message: err.Error()}})
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
			When:    s.When,
			Retry:   s.Retry,
			Notify:  s.Notify,
			Preview: s.Preview,
		}
	}
	resp := ValidateContentResponse{Plan: plan, Steps: steps}
	if mode, merr := cuetry.RecipeExecutionMode(*recipe); merr == nil && mode == cuetry.ExecutionModeGraph {
		if gp, gerr := cuetry.BuildRecipeGraphPlan(*recipe); gerr == nil {
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
// @Param body body GraphPlanRequest true "path or recipe_content"
// @Success 200 {object} cuetry.RecipeGraphPlan
// @Failure 400 {object} map[string]string
// @Router /api/v1/recipes/graph-plan [post]
// @Security BearerAuth
func (*Server) handleRecipesGraphPlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body GraphPlanRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}
	var recipe cuetry.Recipe
	switch {
	case body.RecipeContent != nil:
		parsed, perr := recipeFromContentMap(body.RecipeContent)
		if perr != nil {
			httpError(w, perr, http.StatusBadRequest)
			return
		}
		if parsed == nil {
			httpError(w, fmt.Errorf("recipe_content required"), http.StatusBadRequest)
			return
		}
		recipe = *parsed
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

func writeValidationErrors(w http.ResponseWriter, errs []ValidateContentError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(ValidateContentResponse{Errors: errs})
}

// handleRecipesParse reads and parses a recipe file from an allowed path.
// @Summary Parse recipe file to JSON
// @Tags recipes
// @Accept json
// @Produce json
// @Param body body RecipesParseRequest true "absolute recipe file path"
// @Success 200 {object} RecipesParseResponse
// @Failure 400 {object} map[string]string
// @Router /api/v1/recipes/parse [post]
// @Security BearerAuth
func (*Server) handleRecipesParse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body RecipesParseRequest
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
	b, err := json.Marshal(recipe)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	var recipeMap map[string]interface{}
	if err := json.Unmarshal(b, &recipeMap); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(RecipesParseResponse{Recipe: recipeMap})
}
