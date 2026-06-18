package webserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/plugins"
	"github.com/shareed2k/honey/internal/safepath"
	"github.com/shareed2k/honey/internal/ui"
)

const (
	maxWebExecRecords   = 200
	maxWebExecCommand   = 8192
	maxWebExecScript    = 64 << 10
	maxWebExecArgs      = 64
	maxWebExecArgLen    = 4 << 10
	maxRecipeViewBytes  = 512 << 10
	cueExecChannelCap   = 4096
	execModeCommand     = "command"
	execModeScript      = "script"
	execConnectableHint = "need IP, k8s pod, or docker container"
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
	SSHUser               string         `json:"ssh_user"`
	Command               string         `json:"command"`
	ExecMode              string         `json:"exec_mode,omitempty"`
	ScriptInterpreter     string         `json:"script_interpreter,omitempty"`
	InterpreterArgsQuoted bool           `json:"interpreter_args_quoted,omitempty"`
	FileExtension         string         `json:"file_extension,omitempty"`
	RemoveTmpFile         *bool          `json:"remove_tmp_file,omitempty"`
	RunAs                 string         `json:"run_as,omitempty"`
	ScriptArgs            []string       `json:"script_args,omitempty"`
	Records               []hosts.Record `json:"records"`
	RecordSession         bool           `json:"record_session"`
}

// ExecResponse is the JSON body for a successful exec run.
type ExecResponse struct {
	Results []engine.HostExecResult `json:"results"`
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
	Results []engine.HostExecResult `json:"results"`
}

func (s *Server) allowedRecipePathSet() map[string]struct{} {
	out := make(map[string]struct{})
	for _, p := range config.ListDefaultRecipes() {
		if cp, err := filepath.Abs(filepath.Clean(p)); err == nil {
			out[cp] = struct{}{}
		}
	}
	if s != nil && s.opts.Config != nil {
		for _, app := range s.opts.Config.Apps {
			p := strings.TrimSpace(app.TargetRecipe)
			if p != "" {
				if !filepath.IsAbs(p) && s.opts.ConfigPath != "" {
					p = filepath.Join(filepath.Dir(s.opts.ConfigPath), p)
				}
				if cp, err := filepath.Abs(filepath.Clean(p)); err == nil {
					out[cp] = struct{}{}
				}
			}
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
func (s *Server) handleRecipesList(w http.ResponseWriter, r *http.Request) {
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
func (s *Server) handleRecipesView(w http.ResponseWriter, r *http.Request) {
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
	allowed := s.allowedRecipePathSet()
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

func normalizeExecMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", execModeCommand:
		return execModeCommand, nil
	case execModeScript:
		return execModeScript, nil
	default:
		return "", fmt.Errorf("invalid exec_mode %q (want %q or %q)", mode, execModeCommand, execModeScript)
	}
}

func execRemoveTmpFile(body ExecRequest) bool {
	if body.RemoveTmpFile == nil {
		return true
	}
	return *body.RemoveTmpFile
}

func validateExecFileExtension(ext string) error {
	ext = strings.TrimSpace(ext)
	if ext == "" {
		return nil
	}
	if strings.ContainsAny(ext, `/\`) {
		return fmt.Errorf("file_extension must not contain path separators")
	}
	if len(ext) > 32 {
		return fmt.Errorf("file_extension too long (max 32)")
	}
	return nil
}

func (s *Server) validateExecRequest(body ExecRequest) (string, error) {
	mode, err := normalizeExecMode(body.ExecMode)
	if err != nil {
		return "", err
	}
	cmd := strings.TrimSpace(body.Command)
	if cmd == "" {
		return "", fmt.Errorf("empty command")
	}
	if mode == execModeCommand && len(cmd) > maxWebExecCommand {
		return "", fmt.Errorf("command too long (max %d)", maxWebExecCommand)
	}
	if mode == execModeScript {
		if len(body.Command) > maxWebExecScript {
			return "", fmt.Errorf("script too long (max %d)", maxWebExecScript)
		}
		if err := validateExecFileExtension(body.FileExtension); err != nil {
			return "", err
		}
		if len(strings.TrimSpace(body.ScriptInterpreter)) > 512 {
			return "", fmt.Errorf("script_interpreter too long (max 512)")
		}
	}
	if len(body.ScriptArgs) > maxWebExecArgs {
		return "", fmt.Errorf("too many script_args (max %d)", maxWebExecArgs)
	}
	for _, a := range body.ScriptArgs {
		if len(a) > maxWebExecArgLen {
			return "", fmt.Errorf("script arg too long (max %d)", maxWebExecArgLen)
		}
	}
	if strings.TrimSpace(body.RunAs) != "" {
		if err := cuetry.ValidateRunAsUser(body.RunAs); err != nil {
			return "", err
		}
	}
	if len(body.Records) == 0 {
		return "", fmt.Errorf("no hosts selected")
	}
	if len(body.Records) > maxWebExecRecords {
		return "", fmt.Errorf("too many hosts (max %d)", maxWebExecRecords)
	}
	return mode, nil
}

func (s *Server) handleRecipesSchema(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	schema := cuetry.BuildStepJSONSchema()
	_ = json.NewEncoder(w).Encode(schema)
}

// handleExec runs a shell command on many hosts in parallel (optional NDJSON stream).
// @Summary Parallel remote exec
// @Tags exec
// @Accept json
// @Produce json
// @Param stream query int false "set to 1 for NDJSON streaming"
// @Param body body ExecRequest true "remote exec request"
// @Success 200 {object} ExecResponse "JSON body; NDJSON stream of engine.HostExecResult when stream=1"
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
	mode, err := s.validateExecRequest(body)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	cmd := strings.TrimSpace(body.Command)
	if mode == execModeCommand {
		// Apply run_as (sudo) to command mode; no-op when run_as is empty.
		wrapped, werr := cuetry.WrapRemoteShell(body.RunAs, cmd)
		if werr != nil {
			httpError(w, werr, http.StatusBadRequest)
			return
		}
		cmd = wrapped
	}
	user := s.sshUser(body.SSHUser)
	jobs := filterConnectableRecords(body.Records)
	var scriptUnconnectable []engine.HostExecResult
	if mode == execModeScript {
		scriptUnconnectable = buildUnconnectableExecResults(body.Records)
	} else if len(jobs) == 0 {
		httpError(w, fmt.Errorf("no connectable hosts in selection (%s)", execConnectableHint), http.StatusBadRequest)
		return
	}
	recordJobCount := len(jobs)
	if mode == execModeScript {
		recordJobCount = len(body.Records)
	}
	scriptOpts := engine.ScriptUploadRunOptions{
		ScriptInterpreter:     strings.TrimSpace(body.ScriptInterpreter),
		InterpreterArgsQuoted: body.InterpreterArgsQuoted,
		RemoveRemoteFile:      execRemoveTmpFile(body),
		ScriptArgs:            body.ScriptArgs,
		RunAs:                 strings.TrimSpace(body.RunAs),
	}

	if r.URL.Query().Get("stream") == "1" {
		s.handleExecStream(w, body, mode, cmd, user, jobs, scriptUnconnectable, recordJobCount, scriptOpts)
		return
	}
	s.handleExecSync(w, body, mode, cmd, user, jobs, scriptUnconnectable, recordJobCount, scriptOpts)
}

func (s *Server) handleExecStream(w http.ResponseWriter, body ExecRequest, mode, cmd, user string, jobs []hosts.Record, scriptUnconnectable []engine.HostExecResult, recordJobCount int, scriptOpts engine.ScriptUploadRunOptions) {
	execStart := time.Now()
	var rec *engine.SessionRecorder
	wantRec := strings.TrimSpace(s.opts.RecordDir) != "" && body.RecordSession
	if wantRec {
		var err error
		rec, err = engine.NewBatchSessionRecorder(s.opts.RecordDir, "web-exec", user, recordJobCount)
		if err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		defer func() { _ = rec.Close() }()
	}
	ch := make(chan engine.HostExecResult, len(jobs))
	go func() {
		defer close(ch)
		if mode == execModeScript {
			for i := range scriptUnconnectable {
				ch <- scriptUnconnectable[i]
			}
			if len(jobs) == 0 {
				return
			}
			if err := engine.StreamScriptContentRunParallel(context.Background(), user, jobs, body.Command, body.FileExtension, scriptOpts, ch, engine.BatchOptions{Obs: s.metrics, Reg: s.opts.ExecRegistry}); err != nil {
				ch <- engine.HostExecResult{Name: "web-exec", Provider: "web", Success: false, ErrMsg: err.Error()}
			}
			return
		}
		_ = engine.StreamSSHParallel(context.Background(), user, jobs, false, func(_ hosts.Record, _ map[string]string) string { return cmd }, ch, engine.BatchOptions{Obs: s.metrics, Reg: s.opts.ExecRegistry})
	}()
	streamHostExecNDJSON(w, ch, rec)
	if s.metrics != nil {
		s.metrics.ObserveExecCommand("ok", recordJobCount, time.Since(execStart))
	}
}

func (s *Server) handleExecSync(w http.ResponseWriter, body ExecRequest, mode, cmd, user string, jobs []hosts.Record, scriptUnconnectable []engine.HostExecResult, recordJobCount int, scriptOpts engine.ScriptUploadRunOptions) {
	execStart := time.Now()
	var rec *engine.SessionRecorder
	wantRec := strings.TrimSpace(s.opts.RecordDir) != "" && body.RecordSession
	if wantRec {
		var err error
		rec, err = engine.NewBatchSessionRecorder(s.opts.RecordDir, "web-exec", user, recordJobCount)
		if err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		defer func() { _ = rec.Close() }()
	}
	var results []engine.HostExecResult
	var err error
	if mode == execModeScript {
		results = append(results, scriptUnconnectable...)
		if len(jobs) > 0 {
			var scriptResults []engine.HostExecResult
			scriptResults, err = engine.ExecuteScriptContentRunParallel(user, jobs, body.Command, body.FileExtension, scriptOpts, 0, s.opts.ExecRegistry)
			if err != nil {
				if rec != nil {
					rec.RecordError(err)
				}
				if s.metrics != nil {
					s.metrics.ObserveExecCommand("error", recordJobCount, time.Since(execStart))
				}
				httpError(w, err, http.StatusInternalServerError)
				return
			}
			results = append(results, scriptResults...)
		}
	} else {
		results, err = engine.ExecuteSSHParallel(user, jobs, func(_ hosts.Record) string { return cmd }, 0, s.opts.ExecRegistry)
		if err != nil {
			if rec != nil {
				rec.RecordError(err)
			}
			if s.metrics != nil {
				s.metrics.ObserveExecCommand("error", recordJobCount, time.Since(execStart))
			}
			httpError(w, err, http.StatusInternalServerError)
			return
		}
	}
	if rec != nil {
		for i := range results {
			rec.RecordHostExecResult(results[i])
		}
	}
	if s.metrics != nil {
		status := "ok"
		for _, res := range results {
			if !res.Success && !res.Skipped {
				status = "error"
				break
			}
		}
		s.metrics.ObserveExecCommand(status, recordJobCount, time.Since(execStart))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ExecResponse{Results: results})
}

func buildUnconnectableExecResults(recs []hosts.Record) []engine.HostExecResult {
	var out []engine.HostExecResult
	for i := range recs {
		r := recs[i]
		if r.IsConnectable() {
			continue
		}
		out = append(out, engine.HostExecResult{
			Name:     r.Name,
			IP:       r.PrimaryIP,
			Provider: r.Provider,
			Success:  false,
			ErrMsg:   fmt.Sprintf("record is not connectable (%s)", execConnectableHint),
		})
	}
	return out
}

func filterConnectableRecords(recs []hosts.Record) []hosts.Record {
	var out []hosts.Record
	for _, r := range recs {
		if r.IsConnectable() {
			out = append(out, r)
		}
	}
	return out
}

func streamHostExecNDJSON(w http.ResponseWriter, ch <-chan engine.HostExecResult, rec *engine.SessionRecorder) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	if rec != nil {
		if id := rec.RecordingID(); id != "" {
			w.Header().Set("X-Honey-Recording-Id", id)
		}
	}
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
func (s *Server) resolveCueExecRecipe(body CueExecRequest, records []hosts.Record, parseOpts cuetry.ParseOptions) (cuetry.Recipe, string, string, error) {
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
		allowedPaths := s.allowedRecipePathSet()
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
//
//nolint:gocyclo
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

	recipe, recipeSourcePath, recipeDir, err := s.resolveCueExecRecipe(body, body.Records, cuetry.ParseOptions{PluginManager: pluginMgr})
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
		httpError(w, fmt.Errorf("no connectable hosts in selection (need IP, k8s pod, or docker container)"), http.StatusBadRequest)
		return
	}

	cliEnv, err := cuetry.ParseEnvKeyValuePairs(body.Env)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	mergeK8sDebugImageFromRecipe(recipe, jobs)

	user := s.sshUser(body.SSHUser)
	wantRec := strings.TrimSpace(s.opts.RecordDir) != "" && body.RecordSession

	recordRecipeMeta := func(rec *engine.SessionRecorder) {
		if rec == nil {
			return
		}
		hash, _ := recipe.HashJSON()

		planText, _, _ := cuetry.RenderDryRunPlan(recipe)
		var graph *cuetry.RecipeGraphPlan
		if mode, err := cuetry.RecipeExecutionMode(recipe); err == nil && mode == cuetry.ExecutionModeGraph {
			graph, _ = cuetry.BuildRecipeGraphPlan(recipe)
		}

		rec.RecordRecipeMeta(engine.RecipeMeta{
			RecipePath:        recipeSourcePath,
			HostCount:         len(jobs),
			RecipeContentHash: hash,
			StartedAt:         time.Now().UTC(),
			Hosts:             engine.HostsForRecipeMeta(jobs, maxWebExecRecords),
			Plan:              planText,
			Graph:             graph,
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
		runErr := engine.RunCueRecipeSteps(r.Context(), &buf, engine.CueRecipeRunParams{
			Recipe:         recipe,
			RecipeDir:      recipeDir,
			Records:        jobs,
			SSHUser:        user,
			CLIEnv:         cliEnv,
			ConfigPath:     s.opts.ConfigPath,
			AISystemPrompt: aiPrompt,
			SecretResolver: secRes,
			PluginMgr:      pluginMgr,
			Execute:        false,
			Obs:            s.metrics,
			Reg:            s.opts.ExecRegistry,
			Pools:          s.pgPools,
		}, nil)
		var rec *engine.SessionRecorder
		if wantRec {
			var err error
			rec, err = engine.NewBatchSessionRecorder(s.opts.RecordDir, "web-cue-exec-dry", user, len(jobs))
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
		var rec *engine.SessionRecorder
		if wantRec {
			var err error
			rec, err = engine.NewBatchSessionRecorder(s.opts.RecordDir, "web-cue-exec", user, len(jobs))
			if err != nil {
				httpError(w, err, http.StatusInternalServerError)
				return
			}
			defer func() { _ = rec.Close() }()
			recordRecipeMeta(rec)
		}
		ch := make(chan engine.HostExecResult, cueExecChannelCap)
		go func() {
			defer close(ch)
			aiPrompt := ui.LoadAISystemPromptFromConfigPath(s.opts.ConfigPath)
			if err := engine.StreamCueRecipeSteps(r.Context(), engine.CueRecipeRunParams{
				Recipe:         recipe,
				RecipeDir:      recipeDir,
				Records:        jobs,
				SSHUser:        user,
				CLIEnv:         cliEnv,
				ConfigPath:     s.opts.ConfigPath,
				AISystemPrompt: aiPrompt,
				SecretResolver: secRes,
				PluginMgr:      pluginMgr,
				Execute:        true,
				Obs:            s.metrics,
				Reg:            s.opts.ExecRegistry,
				Pools:          s.pgPools,
			}, ch); err != nil {
				ch <- engine.HostExecResult{
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

	var rec *engine.SessionRecorder
	if wantRec {
		var err error
		rec, err = engine.NewBatchSessionRecorder(s.opts.RecordDir, "web-cue-exec", user, len(jobs))
		if err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		defer func() { _ = rec.Close() }()
		recordRecipeMeta(rec)
	}
	ch := make(chan engine.HostExecResult, cueExecChannelCap)
	errCh := make(chan error, 1)
	go func() {
		defer close(ch)
		aiPrompt := ui.LoadAISystemPromptFromConfigPath(s.opts.ConfigPath)
		errCh <- engine.StreamCueRecipeSteps(r.Context(), engine.CueRecipeRunParams{
			Recipe:         recipe,
			RecipeDir:      recipeDir,
			Records:        jobs,
			SSHUser:        user,
			CLIEnv:         cliEnv,
			ConfigPath:     s.opts.ConfigPath,
			AISystemPrompt: aiPrompt,
			SecretResolver: secRes,
			PluginMgr:      pluginMgr,
			Execute:        true,
			Obs:            s.metrics,
			Reg:            s.opts.ExecRegistry,
			Pools:          s.pgPools,
		}, ch)
	}()
	var results []engine.HostExecResult
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
