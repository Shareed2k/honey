package webserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/safepath"
	"github.com/shareed2k/honey/internal/ui"
)

const (
	maxWebExecRecords  = 200
	maxWebExecCommand  = 8192
	maxRecipeViewBytes = 512 << 10
	cueExecChannelCap  = 4096
)

type recipeListEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type recipesListResponse struct {
	Recipes []recipeListEntry `json:"recipes"`
}

type recipeViewRequest struct {
	Path string `json:"path"`
}

type recipeViewResponse struct {
	Content string `json:"content"`
}

type execRequest struct {
	SSHUser string         `json:"ssh_user"`
	Command string         `json:"command"`
	Records []hosts.Record `json:"records"`
}

type execResponse struct {
	Results []ui.HostExecResult `json:"results"`
}

type cueExecRequest struct {
	RecipePath string         `json:"recipe_path"`
	Execute    bool           `json:"execute"`
	SSHUser    string         `json:"ssh_user"`
	Records    []hosts.Record `json:"records"`
	Env        []string       `json:"env,omitempty"`
}

type cueExecDryRunResponse struct {
	Plan string `json:"plan"`
}

type cueExecExecuteResponse struct {
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

func (*Server) handleRecipesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	paths := config.ListDefaultRecipes()
	seen := make(map[string]struct{})
	var recipes []recipeListEntry
	for _, p := range paths {
		cp, err := filepath.Abs(filepath.Clean(p))
		if err != nil {
			continue
		}
		if _, ok := seen[cp]; ok {
			continue
		}
		seen[cp] = struct{}{}
		recipes = append(recipes, recipeListEntry{
			Name: filepath.Base(cp),
			Path: cp,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(recipesListResponse{Recipes: recipes})
}

func (*Server) handleRecipesView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body recipeViewRequest
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
	_ = json.NewEncoder(w).Encode(recipeViewResponse{Content: string(raw)})
}

func (*Server) handleExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body execRequest
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
	if r.URL.Query().Get("stream") == "1" {
		ch := make(chan ui.HostExecResult, len(jobs))
		go func() {
			defer close(ch)
			_ = ui.StreamSSHParallel(user, jobs, func(_ hosts.Record) string { return cmd }, 0, ch, nil)
		}()
		streamHostExecNDJSON(w, ch)
		return
	}
	results, err := ui.ExecuteSSHParallel(user, jobs, func(_ hosts.Record) string { return cmd }, 0)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(execResponse{Results: results})
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

func streamHostExecNDJSON(w http.ResponseWriter, ch <-chan ui.HostExecResult) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	fl, _ := w.(http.Flusher)
	for res := range ch {
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

func (*Server) handleCueExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body cueExecRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<20)).Decode(&body); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}
	cp, err := normalizeRecipePath(body.RecipePath)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	allowedPaths := allowedRecipePathSet()
	if _, ok := allowedPaths[cp]; !ok {
		httpError(w, fmt.Errorf("recipe_path not allowed"), http.StatusBadRequest)
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

	raw, err := safepath.ReadFile(cp)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	recipe, err := cuetry.ParseRemoteRecipe(raw, jobs)
	if err != nil {
		httpError(w, fmt.Errorf("parse recipe: %w", err), http.StatusBadRequest)
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
	recipeDir := filepath.Dir(cp)

	if !body.Execute {
		var buf bytes.Buffer
		if err := ui.RunCueRecipeSteps(&buf, recipe, recipeDir, jobs, user, false, cliEnv); err != nil {
			httpError(w, err, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cueExecDryRunResponse{Plan: buf.String()})
		return
	}
	if r.URL.Query().Get("stream") == "1" {
		ch := make(chan ui.HostExecResult, cueExecChannelCap)
		go func() {
			defer close(ch)
			if err := ui.StreamCueRecipeSteps(recipe, recipeDir, jobs, user, cliEnv, ch); err != nil {
				ch <- ui.HostExecResult{
					Name:     "cue-exec",
					Provider: "web",
					Success:  false,
					ErrMsg:   err.Error(),
				}
			}
		}()
		streamHostExecNDJSON(w, ch)
		return
	}

	ch := make(chan ui.HostExecResult, cueExecChannelCap)
	errCh := make(chan error, 1)
	go func() {
		defer close(ch)
		errCh <- ui.StreamCueRecipeSteps(recipe, recipeDir, jobs, user, cliEnv, ch)
	}()
	var results []ui.HostExecResult
	for res := range ch {
		results = append(results, res)
	}
	if err := <-errCh; err != nil {
		httpError(w, err, http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cueExecExecuteResponse{Results: results})
}
