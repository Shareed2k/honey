package webserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/webserver/recipestore"
)

func (s *Server) recipeStore(_ *http.Request, gitOpts *config.StudioConfig) recipestore.RecipeStore {
	var dir string
	switch {
	case s.opts.Config != nil && s.opts.Config.Defaults.Studio.RecipesPath != "":
		dir = s.opts.Config.Defaults.Studio.RecipesPath
	case s.opts.ConfigPath != "":
		dir = filepath.Join(filepath.Dir(s.opts.ConfigPath), "recipes")
	default:
		dir = "examples/recipe"
	}

	var gitCfg config.StudioConfig
	if gitOpts != nil && gitOpts.GitURL != "" {
		gitCfg = *gitOpts
	} else if s.opts.Config != nil {
		gitCfg = s.opts.Config.Defaults.Studio
	}

	if gitCfg.GitURL != "" {
		localPath := filepath.Join(filepath.Dir(dir), "git-store")
		return recipestore.NewGitRecipeStore(gitCfg.GitURL, gitCfg.GitBranch, gitCfg.GitUser, gitCfg.GitPass, gitCfg.GitSSH, localPath)
	}

	return recipestore.NewLocalRecipeStore(dir)
}

func (s *Server) handleRecipesStoreList(w http.ResponseWriter, r *http.Request) {
	store := s.recipeStore(r, nil)
	list, err := store.List(r.Context())
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

func (s *Server) handleRecipesStoreGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if strings.TrimSpace(name) == "" {
		httpError(w, fmt.Errorf("recipe name is required"), http.StatusBadRequest)
		return
	}
	store := s.recipeStore(r, nil)
	content, err := store.Get(r.Context(), name)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(content))
}

type saveRecipeRequest struct {
	Content string `json:"content"`
	config.StudioConfig
}

func (s *Server) handleRecipesStoreSave(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if strings.TrimSpace(name) == "" {
		httpError(w, fmt.Errorf("recipe name is required"), http.StatusBadRequest)
		return
	}
	var req saveRecipeRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	store := s.recipeStore(r, &req.StudioConfig)
	if err := store.Save(r.Context(), name, req.Content); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"success":true}`))
}

func (s *Server) handleRecipesStoreDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if strings.TrimSpace(name) == "" {
		httpError(w, fmt.Errorf("recipe name is required"), http.StatusBadRequest)
		return
	}
	store := s.recipeStore(r, nil)
	if err := store.Delete(r.Context(), name); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
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

func (s *Server) handleRecipesStoreGitList(w http.ResponseWriter, r *http.Request) {
	var req config.StudioConfig
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil && err != io.EOF {
		httpError(w, err, http.StatusBadRequest)
		return
	}

	store := s.recipeStore(r, &req)
	list, err := store.List(r.Context())
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

func (s *Server) handleRecipesStoreGitLoad(w http.ResponseWriter, r *http.Request) {
	var req gitLoadRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.Path) == "" {
		httpError(w, fmt.Errorf("path is required"), http.StatusBadRequest)
		return
	}

	store := s.recipeStore(r, &req.StudioConfig)
	content, err := store.Get(r.Context(), req.Path)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(gitLoadResponse{Content: content})
}

func (s *Server) handleRecipesStudioConfig(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	cfg := s.opts.Config
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
