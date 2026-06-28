package webserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/webserver/recipestore"
)

func (api *RecipesAPI) recipeStore(_ *http.Request, gitOpts *config.StudioConfig) recipestore.RecipeStore {
	var dir string
	switch {
	case api.opts.Config != nil && api.opts.Config.Defaults.Studio.RecipesPath != "":
		dir = api.opts.Config.Defaults.Studio.RecipesPath
	case api.opts.ConfigPath != "":
		dir = filepath.Join(filepath.Dir(api.opts.ConfigPath), "recipes")
	default:
		dir = "examples/recipe"
	}

	var gitCfg config.StudioConfig
	if gitOpts != nil && gitOpts.GitURL != "" {
		gitCfg = *gitOpts
	} else if api.opts.Config != nil {
		gitCfg = api.opts.Config.Defaults.Studio
	}

	if gitCfg.GitURL != "" {
		localPath := filepath.Join(filepath.Dir(dir), "git-store")
		return recipestore.NewGitRecipeStore(gitCfg.GitURL, gitCfg.GitBranch, gitCfg.GitUser, gitCfg.GitPass, gitCfg.GitSSH, localPath)
	}

	return recipestore.NewLocalRecipeStore(dir)
}

func (api *RecipesAPI) handleRecipesStoreList(w http.ResponseWriter, r *http.Request) {
	store := api.recipeStore(r, nil)
	list, err := store.List(r.Context())
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

// StoreLoadResponse is returned by GET /api/v1/recipes/store/{name}.
// It combines the parsed recipe with graph/plan data so the Studio needs only one call.
type StoreLoadResponse struct {
	Recipe map[string]interface{}  `json:"recipe"`
	RawCUE string                  `json:"raw_cue"`
	Plan   string                  `json:"plan,omitempty"`
	Steps  []ResolvedStepSummary   `json:"steps,omitempty"`
	Graph  *cuetry.RecipeGraphPlan `json:"graph,omitempty"`
	Errors []ValidateContentError  `json:"errors,omitempty"`
}

func (api *RecipesAPI) handleRecipesStoreGet(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if strings.TrimSpace(name) == "" {
		httpError(w, fmt.Errorf("recipe name is required"), http.StatusBadRequest)
		return
	}
	store := api.recipeStore(r, nil)
	content, err := store.Get(r.Context(), name)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	recipe, err := cuetry.ParseRemoteRecipe([]byte(content), nil)
	if err != nil {
		httpError(w, fmt.Errorf("parse recipe: %w", err), http.StatusUnprocessableEntity)
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
	resp := StoreLoadResponse{Recipe: recipeMap, RawCUE: content}
	if verr := cuetry.ValidateParsedRecipe(recipe, nil); verr != nil {
		resp.Errors = []ValidateContentError{{Kind: "validation", Message: verr.Error()}}
	} else {
		plan, summaries, perr := cuetry.RenderDryRunPlan(recipe)
		if perr == nil {
			resp.Plan = plan
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
			resp.Steps = steps
		}
		if mode, merr := cuetry.RecipeExecutionMode(recipe); merr == nil && mode == cuetry.ExecutionModeGraph {
			if gp, gerr := cuetry.BuildRecipeGraphPlan(recipe); gerr == nil {
				resp.Graph = gp
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

type saveRecipeRequest struct {
	Content string `json:"content"`
	config.StudioConfig
}

func (api *RecipesAPI) handleRecipesStoreSave(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if strings.TrimSpace(name) == "" {
		httpError(w, fmt.Errorf("recipe name is required"), http.StatusBadRequest)
		return
	}
	var req saveRecipeRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	store := api.recipeStore(r, &req.StudioConfig)
	if err := store.Save(r.Context(), name, req.Content); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	_ = api.opts.AuditSink.Log(r.Context(), audit.Event{
		Source: "web",
		Actor:  actorFromCtx(r.Context()),
		Action: "recipe_save",
		Target: name,
	})
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"success":true}`))
}

func (api *RecipesAPI) handleRecipesStoreDelete(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if strings.TrimSpace(name) == "" {
		httpError(w, fmt.Errorf("recipe name is required"), http.StatusBadRequest)
		return
	}
	store := api.recipeStore(r, nil)
	if err := store.Delete(r.Context(), name); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	_ = api.opts.AuditSink.Log(r.Context(), audit.Event{
		Source: "web",
		Actor:  actorFromCtx(r.Context()),
		Action: "recipe_delete",
		Target: name,
	})
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"success":true}`))
}

type gitLoadRequest struct {
	Path string `json:"path"`
	config.StudioConfig
}

type gitLoadResponse struct {
	Content string `json:"content"`
}

func (api *RecipesAPI) handleRecipesStoreGitList(w http.ResponseWriter, r *http.Request) {
	var req config.StudioConfig
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil && err != io.EOF {
		httpError(w, err, http.StatusBadRequest)
		return
	}

	store := api.recipeStore(r, &req)
	list, err := store.List(r.Context())
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

func (api *RecipesAPI) handleRecipesStoreGitLoad(w http.ResponseWriter, r *http.Request) {
	var req gitLoadRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.Path) == "" {
		httpError(w, fmt.Errorf("path is required"), http.StatusBadRequest)
		return
	}

	store := api.recipeStore(r, &req.StudioConfig)
	content, err := store.Get(r.Context(), req.Path)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(gitLoadResponse{Content: content})
}

func (api *RecipesAPI) handleRecipesStudioConfig(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	cfg := api.opts.Config
	resp := map[string]any{}
	if cfg != nil {
		resp["recipes_path"] = cfg.Defaults.Studio.RecipesPath
		resp["git_url"] = cfg.Defaults.Studio.GitURL
		resp["git_branch"] = cfg.Defaults.Studio.GitBranch
		resp["git_user"] = cfg.Defaults.Studio.GitUser
		resp["git_pass_configured"] = cfg.Defaults.Studio.GitPass != ""
		resp["git_ssh_configured"] = cfg.Defaults.Studio.GitSSH != ""
	} else {
		resp["recipes_path"] = ""
		resp["git_url"] = ""
		resp["git_branch"] = ""
		resp["git_user"] = ""
		resp["git_pass_configured"] = false
		resp["git_ssh_configured"] = false
	}
	_ = json.NewEncoder(w).Encode(resp)
}
