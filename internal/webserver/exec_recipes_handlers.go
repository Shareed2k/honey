package webserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/plugins"
	"github.com/shareed2k/honey/internal/safepath"
	"github.com/shareed2k/honey/internal/ui"
)

const (
	maxWebExecRecords  = 200
	maxWebExecCommand  = 8192
	maxRecipeViewBytes = 512 << 10
	cueExecChannelCap  = 4096
)

// RecipeListEntry is one recipe in the list API response.
type RecipeListEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// RecipesListResponse is the JSON body for GET /api/v1/recipes.
type RecipesListResponse struct {
	Recipes []RecipeListEntry `json:"recipes"`
}

// RecipeViewRequest is the JSON body for POST /api/v1/recipes/view.
type RecipeViewRequest struct {
	Path string `json:"path"`
}

// RecipeViewResponse is the JSON body for a successful recipe view.
type RecipeViewResponse struct {
	Content string `json:"content"`
}

// ExecRequest is the JSON body for POST /api/v1/exec.
type ExecRequest struct {
	SSHUser       string         `json:"ssh_user"`
	Command       string         `json:"command"`
	Records       []hosts.Record `json:"records"`
	RecordSession bool           `json:"record_session"`
}

// ExecResponse is the JSON body for a successful exec run.
type ExecResponse struct {
	Results []ui.HostExecResult `json:"results"`
}

// CueExecRequest is the JSON body for POST /api/v1/cue-exec.
type CueExecRequest struct {
	RecipePath    string                 `json:"recipe_path,omitempty"`
	RecipeContent map[string]interface{} `json:"recipe_content,omitempty"`
	Execute       bool                   `json:"execute"`
	SSHUser       string                 `json:"ssh_user"`
	Records       []hosts.Record         `json:"records"`
	Env           []string               `json:"env,omitempty"`
	RecordSession bool                   `json:"record_session"`
}

// CueExecDryRunResponse is the JSON body when cue-exec runs in dry-run mode.
type CueExecDryRunResponse struct {
	Plan string `json:"plan"`
}

// CueExecExecuteResponse is the JSON body when cue-exec runs with execute true.
type CueExecExecuteResponse struct {
	Results []ui.HostExecResult `json:"results"`
}

func allowedRecipePathSet() map[string]struct{} {
	out := make(map[string]struct{})
	for _, p := range config.ListDefaultRecipes() {
		if cp, err := filepath.Abs(filepath.Clean(p)); err == nil {
			out[cp] = struct{}{}
		}
	}
	return out
}

func normalizeRecipePath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	return filepath.Abs(filepath.Clean(p))
}

// handleRecipesList returns discoverable recipe file paths.
// @Summary List recipe files
// @Tags recipes
// @Produce json
// @Success 200 {object} RecipesListResponse
// @Router /api/v1/recipes [get]
// @Security BearerAuth
func (*Server) handleRecipesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	paths := config.ListDefaultRecipes()
	seen := make(map[string]struct{})
	var recipes []RecipeListEntry
	for _, p := range paths {
		cp, err := filepath.Abs(filepath.Clean(p))
		if err != nil {
			continue
		}
		if _, ok := seen[cp]; ok {
			continue
		}
		seen[cp] = struct{}{}
		recipes = append(recipes, RecipeListEntry{
			Name: filepath.Base(cp),
			Path: cp,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(RecipesListResponse{Recipes: recipes})
}

// handleRecipesView reads a recipe file from an allowed path.
// @Summary Read recipe file
// @Tags recipes
// @Accept json
// @Produce json
// @Param body body RecipeViewRequest true "absolute or resolved recipe path"
// @Success 200 {object} RecipeViewResponse
// @Failure 400 {object} map[string]string
// @Router /api/v1/recipes/view [post]
// @Security BearerAuth
func (*Server) handleRecipesView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body RecipeViewRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}
	cp, err := normalizeRecipePath(body.Path)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	allowed := allowedRecipePathSet()
	if _, ok := allowed[cp]; !ok {
		httpError(w, fmt.Errorf("recipe path not allowed"), http.StatusBadRequest)
		return
	}
	raw, err := safepath.ReadFile(cp)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if len(raw) > maxRecipeViewBytes {
		httpError(w, fmt.Errorf("recipe file too large (max %d bytes)", maxRecipeViewBytes), http.StatusBadRequest)
		return
	}
	if !utf8.Valid(raw) {
		httpError(w, fmt.Errorf("recipe is not valid UTF-8"), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(RecipeViewResponse{Content: string(raw)})
}

// handleExec runs a shell command on many hosts in parallel (optional NDJSON stream).
// @Summary Parallel remote exec
// @Tags exec
// @Accept json
// @Produce json
// @Param stream query int false "set to 1 for NDJSON streaming"
// @Param body body ExecRequest true "remote exec request"
// @Success 200 {object} ExecResponse "JSON body; NDJSON stream of ui.HostExecResult when stream=1"
// @Failure 400 {object} map[string]string
// @Router /api/v1/exec [post]
// @Security BearerAuth
func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body ExecRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&body); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}
	cmd := strings.TrimSpace(body.Command)
	if cmd == "" {
		httpError(w, fmt.Errorf("empty command"), http.StatusBadRequest)
		return
	}
	if len(cmd) > maxWebExecCommand {
		httpError(w, fmt.Errorf("command too long (max %d)", maxWebExecCommand), http.StatusBadRequest)
		return
	}
	if len(body.Records) == 0 {
		httpError(w, fmt.Errorf("no hosts selected"), http.StatusBadRequest)
		return
	}
	if len(body.Records) > maxWebExecRecords {
		httpError(w, fmt.Errorf("too many hosts (max %d)", maxWebExecRecords), http.StatusBadRequest)
		return
	}
	user := strings.TrimSpace(body.SSHUser)
	if user == "" {
		user = os.Getenv("USER")
	}
	jobs := filterConnectableRecords(body.Records)
	if len(jobs) == 0 {
		httpError(w, fmt.Errorf("no connectable hosts in selection (need IP or k8s pod)"), http.StatusBadRequest)
		return
	}
	wantRec := strings.TrimSpace(s.opts.RecordDir) != "" && body.RecordSession

	if r.URL.Query().Get("stream") == "1" {
		var rec *ui.SessionRecorder
		if wantRec {
			var err error
			rec, err = ui.NewBatchSessionRecorder(s.opts.RecordDir, "web-exec", user, len(jobs))
			if err != nil {
				httpError(w, err, http.StatusInternalServerError)
				return
			}
			defer func() { _ = rec.Close() }()
		}
		ch := make(chan ui.HostExecResult, len(jobs))
		go func() {
			defer close(ch)
			_ = ui.StreamSSHParallel(context.Background(), user, jobs, false, func(_ hosts.Record, _ map[string]string) string { return cmd }, 0, ch, nil, nil, false, nil)
		}()
		streamHostExecNDJSON(w, ch, rec)
		return
	}
	var rec *ui.SessionRecorder
	if wantRec {
		var err error
		rec, err = ui.NewBatchSessionRecorder(s.opts.RecordDir, "web-exec", user, len(jobs))
		if err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		defer func() { _ = rec.Close() }()
	}
	results, err := ui.ExecuteSSHParallel(user, jobs, func(_ hosts.Record) string { return cmd }, 0)
	if err != nil {
		if rec != nil {
			rec.RecordError(err)
		}
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	if rec != nil {
		for i := range results {
			rec.RecordHostExecResult(results[i])
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ExecResponse{Results: results})
}

func filterConnectableRecords(recs []hosts.Record) []hosts.Record {
	var out []hosts.Record
	for _, r := range recs {
		if strings.TrimSpace(r.PrimaryIP) != "" || (r.Provider == "k8s" && strings.EqualFold(r.Meta["kind"], "pod")) {
			out = append(out, r)
		}
	}
	return out
}

func streamHostExecNDJSON(w http.ResponseWriter, ch <-chan ui.HostExecResult, rec *ui.SessionRecorder) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	fl, _ := w.(http.Flusher)
	for res := range ch {
		if rec != nil {
			rec.RecordHostExecResult(res)
		}
		if err := enc.Encode(res); err != nil {
			return
		}
		if fl != nil {
			fl.Flush()
		}
	}
}

func mergeK8sDebugImageFromRecipe(recipe cuetry.Recipe, records []hosts.Record) {
	if recipe.Defaults == nil || strings.TrimSpace(recipe.Defaults.K8sDebugImage) == "" {
		return
	}
	img := strings.TrimSpace(recipe.Defaults.K8sDebugImage)
	for i := range records {
		if records[i].Provider == "k8s" && strings.EqualFold(records[i].Meta["kind"], "pod") {
			if records[i].Meta == nil {
				records[i].Meta = make(map[string]string)
			}
			records[i].Meta["debug_image"] = img
		}
	}
}

// resolveCueExecRecipe picks the recipe source from a CueExecRequest, returning
// the parsed/validated recipe, its on-disk source path (empty for inline),
// and the recipe's directory (empty for inline). All errors are caller-fixable
// (HTTP 400 from the handler).
func resolveCueExecRecipe(body CueExecRequest, records []hosts.Record, parseOpts cuetry.ParseOptions) (cuetry.Recipe, string, string, error) {
	switch {
	case body.RecipeContent != nil:
		if strings.TrimSpace(body.RecipePath) != "" {
			return cuetry.Recipe{}, "", "", fmt.Errorf("recipe_path and recipe_content are mutually exclusive")
		}
		parsed, err := recipeFromContentMap(body.RecipeContent)
		if err != nil {
			return cuetry.Recipe{}, "", "", err
		}
		if parsed == nil {
			return cuetry.Recipe{}, "", "", fmt.Errorf("recipe_content required")
		}
		recipe := *parsed
		if err := cuetry.ValidateParsedRecipe(recipe, records); err != nil {
			return cuetry.Recipe{}, "", "", fmt.Errorf("recipe_content: %w", err)
		}
		return recipe, "", "", nil
	case strings.TrimSpace(body.RecipePath) != "":
		cp, err := normalizeRecipePath(body.RecipePath)
		if err != nil {
			return cuetry.Recipe{}, "", "", err
		}
		allowedPaths := allowedRecipePathSet()
		if _, ok := allowedPaths[cp]; !ok {
			return cuetry.Recipe{}, "", "", fmt.Errorf("recipe_path not allowed")
		}
		raw, err := safepath.ReadFile(cp)
		if err != nil {
			return cuetry.Recipe{}, "", "", err
		}
		parsed, err := cuetry.ParseRemoteRecipeOpts(raw, records, parseOpts)
		if err != nil {
			return cuetry.Recipe{}, "", "", fmt.Errorf("parse recipe: %w", err)
		}
		return parsed, cp, filepath.Dir(cp), nil
	default:
		return cuetry.Recipe{}, "", "", fmt.Errorf("recipe_path or recipe_content required")
	}
}

// handleCueExec validates or runs a CUE recipe against selected hosts.
// @Summary CUE recipe dry-run or execute
// @Tags recipes
// @Accept json
// @Produce json
// @Param stream query int false "set to 1 for NDJSON streaming when execute=true"
// @Param body body CueExecRequest true "cue-exec request"
// @Success 200 {object} CueExecDryRunResponse "dry-run plan when execute=false"
// @Success 200 {object} CueExecExecuteResponse "host results when execute=true and stream is not set"
// @Failure 400 {object} map[string]string
// @Router /api/v1/cue-exec [post]
// @Security BearerAuth
func (s *Server) handleCueExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body CueExecRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<20)).Decode(&body); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}

	pluginMgr, err := plugins.Open(r.Context(), s.opts.Config)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	defer func() { _ = pluginMgr.Close() }()

	recipe, recipeSourcePath, recipeDir, err := resolveCueExecRecipe(body, body.Records, cuetry.ParseOptions{PluginManager: pluginMgr})
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}

	if len(body.Records) == 0 {
		httpError(w, fmt.Errorf("no hosts selected"), http.StatusBadRequest)
		return
	}
	if len(body.Records) > maxWebExecRecords {
		httpError(w, fmt.Errorf("too many hosts (max %d)", maxWebExecRecords), http.StatusBadRequest)
		return
	}
	jobs := filterConnectableRecords(body.Records)
	if len(jobs) == 0 {
		httpError(w, fmt.Errorf("no connectable hosts in selection (need IP or k8s pod)"), http.StatusBadRequest)
		return
	}

	cliEnv, err := cuetry.ParseEnvKeyValuePairs(body.Env)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	mergeK8sDebugImageFromRecipe(recipe, jobs)

	user := strings.TrimSpace(body.SSHUser)
	if user == "" {
		user = os.Getenv("USER")
	}
	wantRec := strings.TrimSpace(s.opts.RecordDir) != "" && body.RecordSession

	recordRecipeMeta := func(rec *ui.SessionRecorder) {
		if rec == nil {
			return
		}
		hash, _ := cuetry.HashRecipeJSON(recipe)
		rec.RecordRecipeMeta(ui.RecipeMeta{
			RecipePath:        recipeSourcePath,
			HostCount:         len(jobs),
			RecipeContentHash: hash,
			StartedAt:         time.Now().UTC(),
		})
	}

	secRes, secErr := cuetry.NewSecretResolverWithPlugins(cuetry.SecretResolverOptionsFromHoney(s.opts.Config), pluginMgr)
	if secErr != nil {
		httpError(w, secErr, http.StatusInternalServerError)
		return
	}

	if !body.Execute {
		var buf bytes.Buffer
		aiPrompt := ui.LoadAISystemPromptFromConfigPath(s.opts.ConfigPath)
		runErr := ui.RunCueRecipeSteps(r.Context(), &buf, recipe, recipeDir, jobs, user, false, cliEnv, s.opts.ConfigPath, aiPrompt, secRes, pluginMgr, nil)
		var rec *ui.SessionRecorder
		if wantRec {
			var err error
			rec, err = ui.NewBatchSessionRecorder(s.opts.RecordDir, "web-cue-exec-dry", user, len(jobs))
			if err != nil {
				httpError(w, err, http.StatusInternalServerError)
				return
			}
			defer func() { _ = rec.Close() }()
			recordRecipeMeta(rec)
		}
		if runErr != nil {
			if rec != nil {
				rec.RecordError(runErr)
			}
			httpError(w, runErr, http.StatusBadRequest)
			return
		}
		if rec != nil {
			plan := buf.String()
			if strings.TrimSpace(plan) == "" {
				rec.RecordData("plan", []byte("(empty plan)"))
			} else {
				rec.RecordData("plan", []byte(plan))
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(CueExecDryRunResponse{Plan: buf.String()})
		return
	}
	if r.URL.Query().Get("stream") == "1" {
		var rec *ui.SessionRecorder
		if wantRec {
			var err error
			rec, err = ui.NewBatchSessionRecorder(s.opts.RecordDir, "web-cue-exec", user, len(jobs))
			if err != nil {
				httpError(w, err, http.StatusInternalServerError)
				return
			}
			defer func() { _ = rec.Close() }()
			recordRecipeMeta(rec)
		}
		ch := make(chan ui.HostExecResult, cueExecChannelCap)
		go func() {
			defer close(ch)
			aiPrompt := ui.LoadAISystemPromptFromConfigPath(s.opts.ConfigPath)
			if err := ui.StreamCueRecipeSteps(r.Context(), recipe, recipeDir, jobs, user, cliEnv, s.opts.ConfigPath, aiPrompt, secRes, pluginMgr, true, ch); err != nil {
				ch <- ui.HostExecResult{
					Name:     "cue-exec",
					Provider: "web",
					Success:  false,
					ErrMsg:   err.Error(),
				}
			}
		}()
		streamHostExecNDJSON(w, ch, rec)
		return
	}

	var rec *ui.SessionRecorder
	if wantRec {
		var err error
		rec, err = ui.NewBatchSessionRecorder(s.opts.RecordDir, "web-cue-exec", user, len(jobs))
		if err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		defer func() { _ = rec.Close() }()
		recordRecipeMeta(rec)
	}
	ch := make(chan ui.HostExecResult, cueExecChannelCap)
	errCh := make(chan error, 1)
	go func() {
		defer close(ch)
		aiPrompt := ui.LoadAISystemPromptFromConfigPath(s.opts.ConfigPath)
		errCh <- ui.StreamCueRecipeSteps(r.Context(), recipe, recipeDir, jobs, user, cliEnv, s.opts.ConfigPath, aiPrompt, secRes, pluginMgr, true, ch)
	}()
	var results []ui.HostExecResult
	for res := range ch {
		if rec != nil {
			rec.RecordHostExecResult(res)
		}
		results = append(results, res)
	}
	if err := <-errCh; err != nil {
		if rec != nil {
			rec.RecordError(err)
		}
		httpError(w, err, http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(CueExecExecuteResponse{Results: results})
}
