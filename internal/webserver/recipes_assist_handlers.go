package webserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shareed2k/honey/internal/engine"

	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/safepath"
	"github.com/shareed2k/honey/internal/ui"
)

const (
	maxRecipeAssistRequestBody = 4 << 20
	maxRecipeAssistRecords     = 50
	maxRecipeAssistCueRunes    = 32000
	maxRecipeAssistPlanRunes   = 32000
)

// RecipesAssistRequest is the JSON body for POST /api/v1/recipes/assist.
type RecipesAssistRequest struct {
	RecipePath string         `json:"recipe_path"`
	Model      string         `json:"model"`
	UserPrompt string         `json:"user_prompt"`
	SSHUser    string         `json:"ssh_user"`
	Records    []hosts.Record `json:"records"`
}

// RecipesAssistResponse is the JSON body for a successful recipe assist reply.
type RecipesAssistResponse struct {
	Reply string `json:"reply"`
}

func capRecipeAssistRecords(recs []hosts.Record) []hosts.Record {
	if len(recs) <= maxRecipeAssistRecords {
		return recs
	}
	out := make([]hosts.Record, maxRecipeAssistRecords)
	copy(out, recs[:maxRecipeAssistRecords])
	return out
}

const recipeAssistSystemPrompt = `You help operators understand Honey "remote recipe" CUE files used from the web UI.
A recipe is CUE with a top-level "recipe" object: name, optional defaults (run_as, env, k8s_debug_image, retry), and steps[].
Each step has host (selector), optional run_as, optional when (CEL: host, steps, secrets, env, execute, kv_get/kv_has), optional retry{attempts, delay_ms, max_delay_ms, backoff: fixed|exponential}, and one of: command, put{local,remote}, get{local,remote}, script{local,remote},
agent_transfer{dest_host, source_path, dest_path, cloud{provider,bucket,...}, optional cloud_backend_ref, keep_object, max_retries, agent_remote_dir},
plus optional env for command/script only.

Rules:
- Base explanations ONLY on the recipe source, parser/validator messages, and dry-run plan/error text provided in the user message. Do not invent steps, hosts, or commands that are not implied there.
- If dry-run output is present, walk through it in order and explain what would run on which targets.
- If only parse/validate errors are present, explain how to fix the CUE or host selection.
- Call out destructive operations (remote commands, put/get/script) and privilege (run_as) when visible.
- This is advisory documentation, not a substitute for running cue eval or dry-run yourself before execute.
- Keep the answer structured (short sections or bullets) unless the user asks for prose.`

func clipRunesForRecipeAssist(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "\n…(truncated)"
}

// handleRecipesAssist asks the configured LLM about a recipe (requires OPENAI_API_KEY).
// @Summary Recipe AI assist
// @Tags recipes
// @Accept json
// @Produce json
// @Param body body RecipesAssistRequest true "recipe assist request"
// @Success 200 {object} RecipesAssistResponse
// @Failure 400 {object} map[string]string
// @Failure 503 {object} map[string]string
// @Router /api/v1/recipes/assist [post]
// @Security BearerAuth
func (api *RecipesAPI) handleRecipesAssist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if assistAPIKey() == "" {
		httpError(w, errors.New("recipe assist is not configured (set OPENAI_API_KEY)"), http.StatusServiceUnavailable)
		return
	}

	var body RecipesAssistRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRecipeAssistRequestBody)).Decode(&body); err != nil {
		httpError(w, fmt.Errorf("invalid JSON: %w", err), http.StatusBadRequest)
		return
	}
	records := capRecipeAssistRecords(body.Records)

	cp, err := normalizeRecipePath(body.RecipePath)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	allowed := api.allowedRecipePathSet()
	if _, ok := allowed[cp]; !ok {
		httpError(w, fmt.Errorf("recipe_path not allowed"), http.StatusBadRequest)
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

	resolveCtx, resolveCancel := context.WithTimeout(r.Context(), 25*time.Second)
	chatModel, err := api.ai.resolveAssistChatModel(resolveCtx, body.Model)
	resolveCancel()
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}

	if !api.ai.allowAssistRequest(clientIP(r), assistRPM()) {
		httpError(w, errors.New("rate limit exceeded; try again in a minute"), http.StatusTooManyRequests)
		return
	}

	jobs := filterConnectableRecords(records)
	cueSrc := clipRunesForRecipeAssist(string(raw), maxRecipeAssistCueRunes)

	var parseNote string
	var planNote string
	var parseSlice []hosts.Record
	if len(jobs) > 0 {
		parseSlice = jobs
	}
	pluginMgr, releasePlugins := api.plugins.Borrow()
	defer releasePlugins()
	recipe, perr := cuetry.ParseRemoteRecipeOpts(raw, parseSlice, cuetry.ParseOptions{PluginManager: pluginMgr})
	if perr != nil {
		parseNote = perr.Error()
		if len(jobs) > 0 {
			if _, err2 := cuetry.ParseRemoteRecipeOpts(raw, nil, cuetry.ParseOptions{PluginManager: pluginMgr}); err2 == nil {
				parseNote += "\n(Schema validates with an empty host list but not with the selected hosts.)"
			}
		}
	} else {
		if len(jobs) > 0 {
			mergeK8sDebugImageFromRecipe(recipe, jobs)
			plan, runErr := api.runner.DryRun(r.Context(), engine.RunRequest{
				Recipe:           recipe,
				RecipeSourcePath: cp,
				RecipeDir:        filepath.Dir(cp),
				Records:          jobs,
				SSHUser:          api.sshUser(body.SSHUser),
				ActorID:          actorFromCtx(r.Context()),
				AISystemPrompt:   ui.LoadAISystemPromptFromConfigPath(api.opts.ConfigPath),
				PluginManager:    pluginMgr,
			})
			if runErr != nil {
				planNote = fmt.Sprintf("Dry-run error: %v\n--- Plan output ---\n%s", runErr, clipRunesForRecipeAssist(plan, maxRecipeAssistPlanRunes))
			} else {
				planNote = clipRunesForRecipeAssist(plan, maxRecipeAssistPlanRunes)
				if strings.TrimSpace(planNote) == "" {
					planNote = "(dry-run produced empty plan)"
				}
			}
		} else {
			planNote = "(no connectable hosts in request — only structural validation above applies; no dry-run plan.)"
		}
	}

	userAsk := strings.TrimSpace(body.UserPrompt)
	if userAsk == "" {
		userAsk = "Explain what this recipe does step by step, what each step would do on targets, and any risks or prerequisites."
	}
	userAsk = clipRunesHead(userAsk, assistMaxUserRunes())

	var b strings.Builder
	_, _ = b.WriteString("Operator question:\n")
	_, _ = b.WriteString(userAsk)
	_, _ = b.WriteString("\n\n--- Recipe path ---\n")
	_, _ = b.WriteString(cp)
	_, _ = b.WriteString("\n\n--- Recipe CUE (truncated) ---\n")
	_, _ = b.WriteString(cueSrc)
	_, _ = b.WriteString("\n\n--- Parse / validate ---\n")
	if parseNote != "" {
		_, _ = b.WriteString(parseNote)
	} else {
		_, _ = b.WriteString("OK (parsed successfully for the given host list).")
	}
	_, _ = b.WriteString("\n\n--- Dry-run ---\n")
	_, _ = b.WriteString(planNote)
	_, _ = fmt.Fprintf(&b, "\n\n--- Selection ---\n%d connectable host(s) used for parse/dry-run (capped at %d).\n", len(jobs), maxRecipeAssistRecords)

	userContent := b.String()
	reply, err := assistCreateChatCompletion(r.Context(), chatModel, recipeAssistSystemPrompt, userContent)
	if err != nil {
		zap.L().Warn("recipe assist upstream error", zap.Error(err))
		httpError(w, fmt.Errorf("upstream error: %v", err), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(RecipesAssistResponse{Reply: reply})
}

// RecipesAssistFixRequest is the JSON body for POST /api/v1/recipes/assist-fix.
type RecipesAssistFixRequest struct {
	RecipeContent map[string]interface{} `json:"recipe_content"`
	Errors        []ValidateContentError `json:"errors"`
	Model         string                 `json:"model"`
}

// RecipesGenerateRequest is the JSON body for POST /api/v1/recipes/generate.
type RecipesGenerateRequest struct {
	Intent string `json:"intent"`
	Model  string `json:"model"`
}

// RecipesAIGraphResponse is the JSON body returned by AI recipe endpoints.
type RecipesAIGraphResponse struct {
	Recipe      map[string]interface{} `json:"recipe"`
	Explanation string                 `json:"explanation,omitempty"`
}

func (api *RecipesAPI) handleRecipesAssistFix(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if assistAPIKey() == "" {
		httpError(w, errors.New("recipe assist is not configured"), http.StatusServiceUnavailable)
		return
	}
	var req RecipesAssistFixRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}

	prompt := recipeAssistSystemPrompt + "\n\nThe following Honey CUE recipe failed validation:\n\nErrors:\n"
	for _, e := range req.Errors {
		prompt += fmt.Sprintf("- [%s] %s (Path: %s)\n", e.Kind, e.Message, e.Path)
	}
	prompt += "\nPlease fix it. Return a JSON object with two keys: `recipe` containing the fixed recipe JSON, and `explanation` containing a short string explaining the fix."

	reply, err := api.ai.callDirectLLM(r.Context(), "", req.Model, prompt)
	if err != nil {
		httpError(w, err, http.StatusBadGateway)
		return
	}

	var parsed RecipesAIGraphResponse
	if err := json.Unmarshal([]byte(reply), &parsed); err != nil {
		httpError(w, fmt.Errorf("AI returned invalid JSON: %w", err), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(parsed)
}

func (api *RecipesAPI) handleRecipesGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if assistAPIKey() == "" {
		httpError(w, errors.New("recipe assist is not configured"), http.StatusServiceUnavailable)
		return
	}
	var req RecipesGenerateRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}

	prompt := recipeAssistSystemPrompt + "\n\n" + fmt.Sprintf("Generate a Honey CUE graph recipe for the following intent: %s\n\nReturn a JSON object with two keys: `recipe` containing the recipe JSON, and `explanation` containing a short explanation of how it works.", req.Intent)

	reply, err := api.ai.callDirectLLM(r.Context(), "", req.Model, prompt)
	if err != nil {
		httpError(w, err, http.StatusBadGateway)
		return
	}

	var parsed RecipesAIGraphResponse
	if err := json.Unmarshal([]byte(reply), &parsed); err != nil {
		httpError(w, fmt.Errorf("AI returned invalid JSON: %w", err), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(parsed)
}
