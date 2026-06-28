package webserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/apps"
	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hostapi"
	"github.com/shareed2k/honey/internal/plugins"
	"github.com/shareed2k/honey/internal/queue"
	"github.com/shareed2k/honey/internal/recordings"
	"github.com/shareed2k/honey/internal/safepath"
	"github.com/shareed2k/honey/internal/ui"
	"github.com/tidwall/gjson"
)

func authenticateWebhook(ctx context.Context, api *RecipesAPI, webhook cuetry.RecipeWebhook, pluginMgr *plugins.Manager, r *http.Request) error {
	authSecret := strings.TrimSpace(webhook.AuthSecret)
	if authSecret == "" {
		return nil
	}

	secRes, err := cuetry.NewSecretResolverWithPlugins(cuetry.SecretResolverOptionsFromHoney(api.opts.Config), pluginMgr)
	if err != nil {
		return fmt.Errorf("init secret resolver: %w", err)
	}

	expected, err := secRes.Resolve(ctx, authSecret)
	if err != nil {
		return fmt.Errorf("resolve auth_secret: %w", err)
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" || authHeader != expected {
		return fmt.Errorf("unauthorized")
	}

	return nil
}

// resolveWebhookActor picks the OPA actor for a webhook run: a trusted-proxy /
// JWT identity if present, else the configured payload gjson path, else the
// synthetic "webhook:<app>" fallback.
func (api *RecipesAPI) resolveWebhookActor(r *http.Request, webhook cuetry.RecipeWebhook, body []byte, appName string) string {
	if u := userFromRequest(r, api.opts.TrustedProxyNets, api.opts.JWTPubKey); u != "api" {
		return u
	}
	if path := strings.TrimSpace(webhook.Actor); path != "" {
		if v := gjson.GetBytes(body, path); v.Exists() {
			if s := strings.TrimSpace(v.String()); s != "" {
				return s
			}
		}
	}
	return "webhook:" + appName
}

func extractWebhookPayload(r *http.Request, webhook cuetry.RecipeWebhook) ([]byte, []string, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, nil, fmt.Errorf("read body: %w", err)
	}

	var cliEnv []string
	if len(webhook.Extract) > 0 {
		if !gjson.ValidBytes(body) {
			return nil, nil, fmt.Errorf("invalid json payload")
		}
		parsedJSON := gjson.ParseBytes(body)
		for envKey, jsonPath := range webhook.Extract {
			res := parsedJSON.Get(jsonPath)
			if res.Exists() {
				cliEnv = append(cliEnv, fmt.Sprintf("%s=%s", envKey, res.String()))
			}
		}
	}
	return body, cliEnv, nil
}

func checkWebhookIdempotency(api *RecipesAPI, webhook cuetry.RecipeWebhook, body []byte, appName, webhookName string, r *http.Request) (string, bool) {
	var idemKey, scopedKey string
	if webhook.IdempotencyKey != "" {
		if strings.HasPrefix(webhook.IdempotencyKey, "header:") {
			headerName := strings.TrimSpace(webhook.IdempotencyKey[7:])
			idemKey = r.Header.Get(headerName)
		} else if gjson.ValidBytes(body) {
			idemKey = gjson.GetBytes(body, webhook.IdempotencyKey).String()
		}
	} else {
		hash := sha256.Sum256(body)
		idemKey = hex.EncodeToString(hash[:])
	}

	if idemKey == "" {
		return "", false
	}

	scopedKey = fmt.Sprintf("%s:%s:%s", appName, webhookName, idemKey)
	ttl := 24 * time.Hour
	if webhook.IdempotencyTTL != "" {
		if parsed, err := time.ParseDuration(webhook.IdempotencyTTL); err == nil {
			ttl = parsed
		}
	}

	api.webhookDedupMu.Lock()
	defer api.webhookDedupMu.Unlock()
	if item := api.webhookDedupCache.Get(scopedKey); item != nil {
		return scopedKey, true
	}

	api.webhookDedupCache.Set(scopedKey, "processing", ttl)
	return scopedKey, false
}

func handleAsyncWebhook(api *RecipesAPI, w http.ResponseWriter, _ *http.Request, appName, webhookName, scopedKey, sshUser string, searchIn *hostapi.SearchHostsInput, recipe cuetry.Recipe, recipePath string, envMap map[string]string, aiPrompt, actor string, _ *plugins.Manager) {
	if api.webhookQueue == nil {
		httpError(w, fmt.Errorf("server queue not configured"), http.StatusInternalServerError)
		return
	}

	var rec *engine.SessionRecorder
	var err error
	if strings.TrimSpace(api.opts.RecordDir) != "" {
		rec, err = engine.NewBatchSessionRecorder(api.opts.RecordDir, "web-webhook-"+webhookName, sshUser, 0)
		if err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
	}

	t := time.Now()
	submitErr := api.webhookQueue.Submit(func() {
		ctx := context.Background()
		defer func() {
			if scopedKey != "" {
				api.webhookDedupCache.Delete(scopedKey)
			}
			if rec != nil {
				_ = rec.Close()
			}
		}()

		searchOut, serr := hostapi.SearchHosts(ctx, searchIn, api.opts.ExecRegistry, api.opts.SearchRegistry)
		if serr != nil {
			if rec != nil {
				rec.RecordError(fmt.Errorf("search hosts: %w", serr))
			}
			return
		}
		if len(searchOut.Records) == 0 {
			if rec != nil {
				rec.RecordError(fmt.Errorf("no target hosts found for app %q", appName))
			}
			return
		}

		if rec != nil {
			hash, _ := recipe.HashJSON()
			rec.RecordRecipeMeta(engine.RecipeMeta{
				RecipePath:        recipePath,
				HostCount:         len(searchOut.Records),
				RecipeContentHash: hash,
				StartedAt:         time.Now().UTC(),
				Hosts:             engine.HostsForRecipeMeta(searchOut.Records, engine.RecipeMetaHostLimit),
			})
		}

		// Inject the pre-created recorder so its ID is known synchronously above;
		// the runner records results into it but does NOT close it (we do, in defer).
		if rerr := api.runner.ExecuteAndWait(ctx, engine.RunRequest{
			Recipe:           recipe,
			RecipeSourcePath: recipePath,
			RecipeDir:        filepath.Dir(recipePath),
			Records:          searchOut.Records,
			SSHUser:          sshUser,
			ActorID:          actor,
			Env:              envMap,
			AISystemPrompt:   aiPrompt,
			Recorder:         rec,
		}); rerr != nil && rec != nil {
			rec.RecordError(rerr)
		}
	})
	zap.L().Debug("webhook stage", zap.String("stage", "enqueue"), zap.Duration("dur", time.Since(t)))
	if submitErr != nil {
		if rec != nil {
			_ = rec.Close()
		}
		if submitErr == queue.ErrQueueFull {
			httpError(w, fmt.Errorf("server busy: webhook queue full"), http.StatusTooManyRequests)
			return
		}
		httpError(w, fmt.Errorf("submit webhook task: %w", submitErr), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	response := map[string]string{"status": "queued"}
	if rec != nil {
		response["id"] = rec.RecordingID()
	}
	_ = json.NewEncoder(w).Encode(response)
}

func handleSyncWebhook(api *RecipesAPI, w http.ResponseWriter, r *http.Request, appName, webhookName, scopedKey, sshUser string, searchIn *hostapi.SearchHostsInput, recipe cuetry.Recipe, recipePath string, envMap map[string]string, aiPrompt, actor string, _ *plugins.Manager) {
	if scopedKey != "" {
		defer api.webhookDedupCache.Delete(scopedKey)
	}

	t := time.Now()
	searchOut, err := hostapi.SearchHosts(r.Context(), searchIn, api.opts.ExecRegistry, api.opts.SearchRegistry)
	zap.L().Debug("webhook stage", zap.String("stage", "search"), zap.Duration("dur", time.Since(t)))
	if err != nil {
		httpError(w, fmt.Errorf("search hosts: %w", err), http.StatusBadRequest)
		return
	}
	if len(searchOut.Records) == 0 {
		httpError(w, fmt.Errorf("no target hosts found for app %q", appName), http.StatusBadRequest)
		return
	}

	ch, err := api.runner.Execute(r.Context(), engine.RunRequest{
		Recipe:           recipe,
		RecipeSourcePath: recipePath,
		RecipeDir:        filepath.Dir(recipePath),
		Records:          searchOut.Records,
		SSHUser:          sshUser,
		ActorID:          actor,
		Env:              envMap,
		AISystemPrompt:   aiPrompt,
		RecordSession:    strings.TrimSpace(api.opts.RecordDir) != "",
		RecordLabel:      "web-webhook-" + webhookName,
	})
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}

	results := make([]engine.HostExecResult, 0, len(searchOut.Records))
	var runFailed bool
	for res := range ch {
		if res.Provider == "engine" && res.Name == "recipe-run" && !res.Success {
			runFailed = true
			continue // synthetic run-failure marker; not a host result
		}
		results = append(results, res)
	}
	if runFailed {
		httpError(w, fmt.Errorf("recipe execution failed"), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(CueExecExecuteResponse{Results: results})
}

// handleRecipeWebhook handles incoming Rundeck-style webhooks for recipe apps.
func (api *RecipesAPI) handleRecipeWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	appName := chi.URLParam(r, "app_name")
	webhookName := chi.URLParam(r, "webhook_name")

	if !api.webhookAllow(appName) {
		http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
		return
	}

	if api.opts.Config == nil || api.opts.Config.Apps == nil {
		httpError(w, fmt.Errorf("no apps configured"), http.StatusNotFound)
		return
	}

	app, ok := api.opts.Config.Apps[appName]
	if !ok || app.Type != apps.AppTypeRecipe || strings.TrimSpace(app.TargetRecipe) == "" {
		httpError(w, fmt.Errorf("app not found or not a valid recipe app"), http.StatusNotFound)
		return
	}

	// 1. Resolve and parse recipe
	recipePath := strings.TrimSpace(app.TargetRecipe)
	if !filepath.IsAbs(recipePath) && api.opts.ConfigPath != "" {
		recipePath = filepath.Join(filepath.Dir(api.opts.ConfigPath), recipePath)
	}

	raw, err := safepath.ReadFile(recipePath)
	if err != nil {
		httpError(w, fmt.Errorf("read target recipe: %w", err), http.StatusBadRequest)
		return
	}

	// Plugin manager: shared, owned by plugincache.Cache — do NOT Close it here.
	t := time.Now()
	pluginMgr, releasePlugins := api.plugins.Borrow()
	defer releasePlugins()
	zap.L().Debug("webhook stage", zap.String("stage", "plugins.Borrow"), zap.Duration("dur", time.Since(t)))

	t = time.Now()
	recipe, err := cuetry.ParseRemoteRecipeOpts(raw, nil, cuetry.ParseOptions{PluginManager: pluginMgr})
	zap.L().Debug("webhook stage", zap.String("stage", "parse"), zap.Duration("dur", time.Since(t)))
	if err != nil {
		httpError(w, fmt.Errorf("parse recipe: %w", err), http.StatusBadRequest)
		return
	}

	webhook, ok := recipe.Webhooks[webhookName]
	if !ok {
		httpError(w, fmt.Errorf("webhook %q not found in recipe", webhookName), http.StatusNotFound)
		return
	}

	// 2. Authentication
	if err := authenticateWebhook(r.Context(), api, webhook, pluginMgr, r); err != nil {
		if err.Error() == "unauthorized" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		} else {
			httpError(w, err, http.StatusInternalServerError)
		}
		return
	}

	// 3. Extract JSON payload
	body, cliEnv, err := extractWebhookPayload(r, webhook)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}

	envMap, err := cuetry.ParseEnvKeyValuePairs(cliEnv)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	envMap, err = cuetry.ValidateAndApplyPromptDefaults(recipe.PromptDefs(), envMap)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}

	sshUser := api.sshUser("")
	aiPrompt := ui.LoadAISystemPromptFromConfigPath(api.opts.ConfigPath)

	// Host search input — shared by both branches.
	searchIn := &hostapi.SearchHostsInput{
		ConfigPath: api.opts.ConfigPath,
		Config:     api.opts.Config,
		Name:       app.Target,
		Providers:  app.Provider,
		Backends:   app.Backend,
	}
	if app.Target == "" && app.TargetRegex != "" {
		searchIn.NameRegex = app.TargetRegex
	}

	// 4. Idempotency and Deduplication
	scopedKey, isDuplicate := checkWebhookIdempotency(api, webhook, body, appName, webhookName, r)
	if isDuplicate {
		zap.L().Info("webhook duplicate detected, skipping",
			zap.String("app", appName),
			zap.String("webhook", webhookName))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "duplicate",
			"msg":    "Event already processed or in progress",
		})
		return
	}

	actor := api.resolveWebhookActor(r, webhook, body, appName)
	_ = api.opts.AuditSink.Log(r.Context(), audit.Event{
		Source:   "webhook",
		Actor:    actor,
		Action:   "recipe_run",
		Target:   recipe.Name,
		Decision: "allow",
	})

	isAsync := webhook.Async != nil && *webhook.Async

	// ---- Async path: return 202 immediately; search + execution run in the queue. ----
	if isAsync {
		handleAsyncWebhook(api, w, r, appName, webhookName, scopedKey, sshUser, searchIn, recipe, recipePath, envMap, aiPrompt, actor, pluginMgr)
		return
	}

	// ---- Synchronous path: search + execute inline, return full results. ----
	handleSyncWebhook(api, w, r, appName, webhookName, scopedKey, sshUser, searchIn, recipe, recipePath, envMap, aiPrompt, actor, pluginMgr)
}

// WebhookResultResponse is returned by GET /api/v1/webhooks/results/{id}
type WebhookResultResponse struct {
	ID        string                  `json:"id"`
	Status    string                  `json:"status"`
	StartedAt string                  `json:"started_at,omitempty"`
	Results   []engine.HostExecResult `json:"results,omitempty"`
}

// handleRecipeWebhookResult returns the async results of a webhook execution from recordings.
func (api *RecipesAPI) handleRecipeWebhookResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		httpError(w, fmt.Errorf("missing id"), http.StatusBadRequest)
		return
	}

	if api.opts.RecordDir == "" {
		httpError(w, fmt.Errorf("session recording is not enabled on this server"), http.StatusNotImplemented)
		return
	}

	events, err := recordings.LoadEvents(api.opts.RecordDir, id+".hrec.jsonl")
	if err != nil {
		httpError(w, fmt.Errorf("read recording: %w", err), http.StatusNotFound)
		return
	}

	resp := WebhookResultResponse{
		ID:      id,
		Status:  "completed",
		Results: make([]engine.HostExecResult, 0),
	}

	for _, ev := range events {
		if ev.Type == "recipe-meta" && len(ev.Result) > 0 {
			var meta engine.RecipeMeta
			if err := json.Unmarshal(ev.Result, &meta); err == nil {
				resp.StartedAt = meta.StartedAt.Format(time.RFC3339)
			}
		} else if ev.Type == "result" && len(ev.Result) > 0 {
			var res engine.HostExecResult
			if err := json.Unmarshal(ev.Result, &res); err == nil {
				resp.Results = append(resp.Results, res)
				if !res.Success && !res.Skipped {
					resp.Status = "failed"
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
