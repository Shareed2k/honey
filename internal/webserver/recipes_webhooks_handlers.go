package webserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
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
	return webhookActorFromPayload(webhook, body, appName)
}

// webhookActorFromPayload resolves the actor from the configured gjson path, else
// the synthetic "webhook:<app>" fallback. Used when there is no request identity.
func webhookActorFromPayload(webhook cuetry.RecipeWebhook, body []byte, appName string) string {
	if path := strings.TrimSpace(webhook.Actor); path != "" {
		if v := gjson.GetBytes(body, path); v.Exists() {
			if s := strings.TrimSpace(v.String()); s != "" {
				return s
			}
		}
	}
	return "webhook:" + appName
}

// extractWebhookEnv applies the webhook's extract map (env-var name → gjson path)
// to an already-read body, producing KEY=VALUE pairs.
func extractWebhookEnv(body []byte, webhook cuetry.RecipeWebhook) ([]string, error) {
	if len(webhook.Extract) == 0 {
		return nil, nil
	}
	if !gjson.ValidBytes(body) {
		return nil, fmt.Errorf("invalid json payload")
	}
	parsedJSON := gjson.ParseBytes(body)
	var cliEnv []string
	for envKey, jsonPath := range webhook.Extract {
		if res := parsedJSON.Get(jsonPath); res.Exists() {
			cliEnv = append(cliEnv, fmt.Sprintf("%s=%s", envKey, res.String()))
		}
	}
	return cliEnv, nil
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

// errWebhookBadTarget marks a webhook failure caused by host search / target
// resolution (client/config issue → HTTP 400) rather than execution (→ 500).
var errWebhookBadTarget = errors.New("webhook target error")

// enqueueWebhookAsync submits the recipe run to the queue and returns the
// recording id (empty when recording is disabled). It does not wait for
// execution; per-host errors are recorded into the session, not returned.
func (api *RecipesAPI) enqueueWebhookAsync(webhookName, sshUser string, searchIn *hostapi.SearchHostsInput, recipe cuetry.Recipe, recipePath string, envMap map[string]string, aiPrompt, actor, scopedKey string) (string, error) {
	if api.webhookQueue == nil {
		return "", fmt.Errorf("server queue not configured")
	}

	var rec *engine.SessionRecorder
	var err error
	if strings.TrimSpace(api.opts.RecordDir) != "" {
		rec, err = engine.NewBatchSessionRecorder(api.opts.RecordDir, "web-webhook-"+webhookName, sshUser, 0)
		if err != nil {
			return "", err
		}
	}

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
				rec.RecordError(fmt.Errorf("no target hosts found"))
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
	if submitErr != nil {
		if rec != nil {
			_ = rec.Close()
		}
		return "", submitErr
	}

	if rec != nil {
		return rec.RecordingID(), nil
	}
	return "", nil
}

// executeWebhookSync runs host search + recipe synchronously and returns the
// per-host results. Search/target failures are wrapped with errWebhookBadTarget.
func (api *RecipesAPI) executeWebhookSync(ctx context.Context, webhookName, sshUser string, searchIn *hostapi.SearchHostsInput, recipe cuetry.Recipe, recipePath string, envMap map[string]string, aiPrompt, actor string) ([]engine.HostExecResult, error) {
	searchOut, err := hostapi.SearchHosts(ctx, searchIn, api.opts.ExecRegistry, api.opts.SearchRegistry)
	if err != nil {
		return nil, errors.Join(errWebhookBadTarget, fmt.Errorf("search hosts: %w", err))
	}
	if len(searchOut.Records) == 0 {
		return nil, errors.Join(errWebhookBadTarget, fmt.Errorf("no target hosts found"))
	}

	ch, err := api.runner.Execute(ctx, engine.RunRequest{
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
		return nil, err
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
		return nil, fmt.Errorf("recipe execution failed")
	}
	return results, nil
}

// handleRecipeWebhook handles incoming Rundeck-style webhooks for recipe apps.
func (api *RecipesAPI) handleRecipeWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	appName := chi.URLParam(r, "app_name")
	webhookName := chi.URLParam(r, "webhook_name")

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

	// Read the payload once so it can be captured even on auth failure.
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		httpError(w, fmt.Errorf("read body: %w", err), http.StatusBadRequest)
		return
	}

	isAsync := webhook.Async != nil && *webhook.Async
	record := func(d WebhookDelivery) {
		d.ID = newDeliveryID()
		d.Source = "live"
		d.ReceivedAt = time.Now().UTC()
		d.RemoteAddr = r.RemoteAddr
		d.ContentType = r.Header.Get("Content-Type")
		d.Body = string(body)
		d.Async = isAsync
		api.webhookCapture.Record(appName, webhookName, d)
	}

	// 2. Authentication
	if err := authenticateWebhook(r.Context(), api, webhook, pluginMgr, r); err != nil {
		if err.Error() == "unauthorized" {
			record(WebhookDelivery{Outcome: "unauthorized"})
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		} else {
			httpError(w, err, http.StatusInternalServerError)
		}
		return
	}

	// 3. Extract JSON payload fields
	cliEnv, err := extractWebhookEnv(body, webhook)
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

	// ---- Async path: return 202 immediately; search + execution run in the queue. ----
	if isAsync {
		var id string
		id, err = api.enqueueWebhookAsync(webhookName, sshUser, searchIn, recipe, recipePath, envMap, aiPrompt, actor, scopedKey)
		if err != nil {
			record(WebhookDelivery{AuthOK: true, Extracted: envMap, Actor: actor, IdempotencyKey: scopedKey, Outcome: "error", Error: err.Error()})
			if errors.Is(err, queue.ErrQueueFull) {
				httpError(w, fmt.Errorf("server busy: webhook queue full"), http.StatusTooManyRequests)
				return
			}
			httpError(w, fmt.Errorf("submit webhook task: %w", err), http.StatusInternalServerError)
			return
		}
		record(WebhookDelivery{AuthOK: true, Extracted: envMap, Actor: actor, IdempotencyKey: scopedKey, Outcome: "queued", ExecID: id})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		resp := map[string]string{"status": "queued"}
		if id != "" {
			resp["id"] = id
		}
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	// ---- Synchronous path: search + execute inline, return full results. ----
	if scopedKey != "" {
		defer api.webhookDedupCache.Delete(scopedKey)
	}
	results, err := api.executeWebhookSync(r.Context(), webhookName, sshUser, searchIn, recipe, recipePath, envMap, aiPrompt, actor)
	if err != nil {
		record(WebhookDelivery{AuthOK: true, Extracted: envMap, Actor: actor, IdempotencyKey: scopedKey, Outcome: "error", Error: err.Error()})
		status := http.StatusInternalServerError
		if errors.Is(err, errWebhookBadTarget) {
			status = http.StatusBadRequest
		}
		httpError(w, err, status)
		return
	}
	record(WebhookDelivery{AuthOK: true, Extracted: envMap, Actor: actor, IdempotencyKey: scopedKey, Outcome: "executed", Results: results})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(CueExecExecuteResponse{Results: results})
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

	results, status, startedAt, _, err := api.loadRecordingResults(id)
	if err != nil {
		httpError(w, fmt.Errorf("read recording: %w", err), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(WebhookResultResponse{
		ID:        id,
		Status:    status,
		StartedAt: startedAt,
		Results:   results,
	})
}

// loadRecordingResults reads a webhook execution recording by id and returns the
// host results, an overall status ("completed", or "failed" if any host failed),
// and the RFC3339 start time. Returns an error if the recording can't be read
// (e.g. the async run hasn't produced it yet).
func (api *RecipesAPI) loadRecordingResults(id string) (results []engine.HostExecResult, status, startedAt, runErr string, err error) {
	if strings.TrimSpace(api.opts.RecordDir) == "" {
		return nil, "", "", "", fmt.Errorf("session recording not enabled")
	}
	events, err := recordings.LoadEvents(api.opts.RecordDir, id+".hrec.jsonl")
	if err != nil {
		return nil, "", "", "", err
	}
	results = make([]engine.HostExecResult, 0)
	status = "completed"
	for _, ev := range events {
		switch {
		case ev.Type == "recipe-meta" && len(ev.Result) > 0:
			var meta engine.RecipeMeta
			if json.Unmarshal(ev.Result, &meta) == nil {
				startedAt = meta.StartedAt.Format(time.RFC3339)
			}
		case ev.Type == "result" && len(ev.Result) > 0:
			var res engine.HostExecResult
			if json.Unmarshal(ev.Result, &res) == nil {
				results = append(results, res)
				if !res.Success && !res.Skipped {
					status = "failed"
				}
			}
		case ev.Type == "error" && len(ev.Result) > 0:
			var eMsg string
			if json.Unmarshal(ev.Result, &eMsg) == nil {
				runErr = eMsg
				status = "failed"
			}
		}
	}
	return results, status, startedAt, runErr, nil
}

// ── Webhook debugging (Rundeck-style): test-send + delivery inspection ──────────

// resolveRecipeAppPath validates that appName is a recipe app and returns the
// app config plus the resolved absolute recipe path.
func (api *RecipesAPI) resolveRecipeAppPath(appName string) (apps.AppConfig, string, int, error) {
	if api.opts.Config == nil || api.opts.Config.Apps == nil {
		return apps.AppConfig{}, "", http.StatusNotFound, fmt.Errorf("no apps configured")
	}
	app, ok := api.opts.Config.Apps[appName]
	if !ok || app.Type != apps.AppTypeRecipe || strings.TrimSpace(app.TargetRecipe) == "" {
		return apps.AppConfig{}, "", http.StatusNotFound, fmt.Errorf("app not found or not a valid recipe app")
	}
	recipePath := strings.TrimSpace(app.TargetRecipe)
	if !filepath.IsAbs(recipePath) && api.opts.ConfigPath != "" {
		recipePath = filepath.Join(filepath.Dir(api.opts.ConfigPath), recipePath)
	}
	return app, recipePath, http.StatusOK, nil
}

// parseRecipeFile reads and parses a recipe file (borrowing a plugin manager for
// the parse). Used by debug + apps-list webhook discovery; not the live path,
// which keeps its borrowed manager for auth.
func (api *RecipesAPI) parseRecipeFile(recipePath string) (cuetry.Recipe, error) {
	raw, err := safepath.ReadFile(recipePath)
	if err != nil {
		return cuetry.Recipe{}, fmt.Errorf("read target recipe: %w", err)
	}
	pluginMgr, release := api.plugins.Borrow()
	defer release()
	recipe, err := cuetry.ParseRemoteRecipeOpts(raw, nil, cuetry.ParseOptions{PluginManager: pluginMgr})
	if err != nil {
		return cuetry.Recipe{}, fmt.Errorf("parse recipe: %w", err)
	}
	return recipe, nil
}

// recipeWebhookNames returns the sorted webhook names declared by a recipe app,
// or nil if it is not a recipe app, has no webhooks, or fails to parse.
func (api *RecipesAPI) recipeWebhookNames(app apps.AppConfig) []string {
	if app.Type != apps.AppTypeRecipe || strings.TrimSpace(app.TargetRecipe) == "" {
		return nil
	}
	recipePath := strings.TrimSpace(app.TargetRecipe)
	if !filepath.IsAbs(recipePath) && api.opts.ConfigPath != "" {
		recipePath = filepath.Join(filepath.Dir(api.opts.ConfigPath), recipePath)
	}
	recipe, err := api.parseRecipeFile(recipePath)
	if err != nil || len(recipe.Webhooks) == 0 {
		return nil
	}
	names := make([]string, 0, len(recipe.Webhooks))
	for name := range recipe.Webhooks {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// webhookDebugIdempotencyDisplay derives the idempotency key the live path would
// use, for display only (header-based keys are unavailable from a test payload).
func webhookDebugIdempotencyDisplay(webhook cuetry.RecipeWebhook, body []byte) string {
	if webhook.IdempotencyKey != "" {
		if strings.HasPrefix(webhook.IdempotencyKey, "header:") {
			return ""
		}
		if gjson.ValidBytes(body) {
			return gjson.GetBytes(body, webhook.IdempotencyKey).String()
		}
		return ""
	}
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:])
}

type webhookDebugRequest struct {
	Payload json.RawMessage `json:"payload"`
	Execute bool            `json:"execute"`
}

type webhookDebugResponse struct {
	AuthOK         bool                    `json:"auth_ok"`
	Extracted      map[string]string       `json:"extracted"`
	Actor          string                  `json:"actor"`
	IdempotencyKey string                  `json:"idempotency_key,omitempty"`
	Async          bool                    `json:"async"`
	Executed       bool                    `json:"executed"`
	Outcome        string                  `json:"outcome"`
	ExecID         string                  `json:"exec_id,omitempty"`
	Results        []engine.HostExecResult `json:"results,omitempty"`
	Error          string                  `json:"error,omitempty"`
}

// handleWebhookDebug previews (dry-run) or executes a webhook against a supplied
// test payload. Authenticated by the web-UI auth middleware; the webhook's own
// auth_secret is applied server-side, so the operator never supplies it.
func (api *RecipesAPI) handleWebhookDebug(w http.ResponseWriter, r *http.Request) {
	appName := chi.URLParam(r, "app_name")
	webhookName := chi.URLParam(r, "webhook_name")

	app, recipePath, status, err := api.resolveRecipeAppPath(appName)
	if err != nil {
		httpError(w, err, status)
		return
	}
	recipe, err := api.parseRecipeFile(recipePath)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	webhook, ok := recipe.Webhooks[webhookName]
	if !ok {
		httpError(w, fmt.Errorf("webhook %q not found in recipe", webhookName), http.StatusNotFound)
		return
	}

	var req webhookDebugRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		httpError(w, fmt.Errorf("decode request: %w", err), http.StatusBadRequest)
		return
	}
	body := []byte(req.Payload)
	if len(body) == 0 {
		body = []byte("{}")
	}

	cliEnv, err := extractWebhookEnv(body, webhook)
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

	actor := webhookActorFromPayload(webhook, body, appName)
	idemKey := webhookDebugIdempotencyDisplay(webhook, body)
	isAsync := webhook.Async != nil && *webhook.Async

	resp := webhookDebugResponse{
		AuthOK:         true, // operator already authenticated to the web UI
		Extracted:      envMap,
		Actor:          actor,
		IdempotencyKey: idemKey,
		Async:          isAsync,
	}
	record := func(source, outcome, execID, errMsg string, results []engine.HostExecResult) {
		api.webhookCapture.Record(appName, webhookName, WebhookDelivery{
			ID:             newDeliveryID(),
			Source:         source,
			ReceivedAt:     time.Now().UTC(),
			ContentType:    "application/json",
			Body:           string(body),
			AuthOK:         true,
			Extracted:      envMap,
			Actor:          actor,
			IdempotencyKey: idemKey,
			Async:          isAsync,
			Outcome:        outcome,
			ExecID:         execID,
			Error:          errMsg,
			Results:        results,
		})
	}

	if !req.Execute {
		resp.Outcome = "dry_run"
		record("dry_run", "dry_run", "", "", nil)
		writeWebhookJSON(w, resp)
		return
	}

	resp.Executed = true
	sshUser := api.sshUser("")
	aiPrompt := ui.LoadAISystemPromptFromConfigPath(api.opts.ConfigPath)
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

	if isAsync {
		id, err := api.enqueueWebhookAsync(webhookName, sshUser, searchIn, recipe, recipePath, envMap, aiPrompt, actor, "")
		if err != nil {
			resp.Outcome, resp.Error = "error", err.Error()
			record("test", "error", "", err.Error(), nil)
			writeWebhookJSON(w, resp)
			return
		}
		resp.Outcome, resp.ExecID = "queued", id
		record("test", "queued", id, "", nil)
		writeWebhookJSON(w, resp)
		return
	}

	results, err := api.executeWebhookSync(r.Context(), webhookName, sshUser, searchIn, recipe, recipePath, envMap, aiPrompt, actor)
	if err != nil {
		resp.Outcome, resp.Error = "error", err.Error()
		record("test", "error", "", err.Error(), nil)
		writeWebhookJSON(w, resp)
		return
	}
	resp.Outcome, resp.Results = "executed", results
	record("test", "executed", "", "", results)
	writeWebhookJSON(w, resp)
}

// handleWebhookDeliveries returns recent captured deliveries for a webhook.
func (api *RecipesAPI) handleWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	appName := chi.URLParam(r, "app_name")
	webhookName := chi.URLParam(r, "webhook_name")

	limit := 20
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	deliveries := api.webhookCapture.List(appName, webhookName, limit)
	// Enrich finished async ("queued") deliveries from their recording so the row
	// shows the final outcome + host results. Capture store is left unchanged.
	for i := range deliveries {
		d := &deliveries[i]
		if d.Outcome != "queued" || d.ExecID == "" {
			continue
		}
		results, status, _, runErr, err := api.loadRecordingResults(d.ExecID)
		// Keep "queued" until the run has actually produced results (or failed);
		// the recording file can exist with only metadata mid-run.
		if err != nil || (len(results) == 0 && status != "failed") {
			continue
		}
		d.Results = results
		if runErr != "" {
			d.Error = runErr
		}
		if status == "failed" {
			d.Outcome = "failed"
		} else {
			d.Outcome = "executed"
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"deliveries": deliveries,
	})
}

func writeWebhookJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
