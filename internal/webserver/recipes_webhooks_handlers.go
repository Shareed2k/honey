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

// handleRecipeWebhook handles incoming Rundeck-style webhooks for recipe apps.
//
//nolint:gocyclo
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

	// Plugin manager: shared, owned by pluginCache — do NOT Close it here.
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
	if authSecret := strings.TrimSpace(webhook.AuthSecret); authSecret != "" {
		secRes, err := cuetry.NewSecretResolverWithPlugins(cuetry.SecretResolverOptionsFromHoney(api.opts.Config), pluginMgr)
		if err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		t = time.Now()
		expected, err := secRes.Resolve(r.Context(), authSecret)
		zap.L().Debug("webhook stage", zap.String("stage", "auth"), zap.Duration("dur", time.Since(t)))
		if err != nil {
			httpError(w, fmt.Errorf("resolve auth_secret: %w", err), http.StatusInternalServerError)
			return
		}
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || authHeader != expected {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
	}

	// 3. Extract JSON payload
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		httpError(w, fmt.Errorf("read body: %w", err), http.StatusBadRequest)
		return
	}

	var cliEnv []string
	if len(webhook.Extract) > 0 {
		if !gjson.ValidBytes(body) {
			httpError(w, fmt.Errorf("invalid json payload"), http.StatusBadRequest)
			return
		}
		parsedJSON := gjson.ParseBytes(body)
		for envKey, jsonPath := range webhook.Extract {
			res := parsedJSON.Get(jsonPath)
			if res.Exists() {
				cliEnv = append(cliEnv, fmt.Sprintf("%s=%s", envKey, res.String()))
			}
		}
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
	var idemKey string
	if webhook.IdempotencyKey != "" {
		if strings.HasPrefix(webhook.IdempotencyKey, "header:") {
			headerName := strings.TrimSpace(webhook.IdempotencyKey[7:])
			idemKey = r.Header.Get(headerName)
		} else if gjson.ValidBytes(body) {
			idemKey = gjson.GetBytes(body, webhook.IdempotencyKey).String()
		}
	} else {
		// Fallback to SHA256 of raw body
		hash := sha256.Sum256(body)
		idemKey = hex.EncodeToString(hash[:])
	}

	if idemKey != "" {
		scopedKey := fmt.Sprintf("%s:%s:%s", appName, webhookName, idemKey)
		ttl := 24 * time.Hour
		if webhook.IdempotencyTTL != "" {
			if parsed, err := time.ParseDuration(webhook.IdempotencyTTL); err == nil {
				ttl = parsed
			}
		}

		api.webhookDedupMu.Lock()
		if item := api.webhookDedupCache.Get(scopedKey); item != nil {
			api.webhookDedupMu.Unlock()
			zap.L().Info("webhook duplicate detected, skipping",
				zap.String("app", appName),
				zap.String("webhook", webhookName),
				zap.String("idempotency_key", idemKey))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "duplicate",
				"msg":    "Event already processed or in progress",
			})
			return
		}

		api.webhookDedupCache.Set(scopedKey, "processing", ttl)
		api.webhookDedupMu.Unlock()

		// Optional: Clear on failure so retries succeed, or set to "done"
		defer func() {
			api.webhookDedupMu.Lock()
			api.webhookDedupCache.Set(scopedKey, "done", ttl)
			api.webhookDedupMu.Unlock()
		}()
	}

	isAsync := webhook.Async != nil && *webhook.Async

	// ---- Async path: return 202 immediately; search + execution run in the queue. ----
	if isAsync {
		if api.webhookQueue == nil {
			httpError(w, fmt.Errorf("server queue not configured"), http.StatusInternalServerError)
			return
		}

		var rec *engine.SessionRecorder
		if strings.TrimSpace(api.opts.RecordDir) != "" {
			rec, err = engine.NewBatchSessionRecorder(api.opts.RecordDir, "web-webhook-"+webhookName, sshUser, 0)
			if err != nil {
				httpError(w, err, http.StatusInternalServerError)
				return
			}
		}

		t = time.Now()
		submitErr := api.webhookQueue.Submit(func() {
			ctx := context.Background()
			mgr, _ := plugins.Open(ctx, api.opts.Config) // async owns its own manager lifecycle
			defer func() {
				if mgr != nil {
					_ = mgr.Close()
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
					Hosts:             engine.HostsForRecipeMeta(searchOut.Records, maxWebExecRecords),
				})
			}

			secRes, _ := cuetry.NewSecretResolverWithPlugins(cuetry.SecretResolverOptionsFromHoney(api.opts.Config), mgr)
			runParams := engine.CueRecipeRunParams{
				Recipe:         recipe,
				RecipeDir:      filepath.Dir(recipePath),
				Records:        searchOut.Records,
				SSHUser:        sshUser,
				CLIEnv:         envMap,
				ConfigPath:     api.opts.ConfigPath,
				AISystemPrompt: aiPrompt,
				SecretResolver: secRes,
				PluginMgr:      mgr,
				Execute:        true,
				Obs:            api.metrics,
				Reg:            api.opts.ExecRegistry,
				Pools:          api.pgPools,
				Cache:          api.sshCache,
			}

			ch := make(chan engine.HostExecResult, cueExecChannelCap)
			go func() {
				defer close(ch)
				_ = engine.StreamCueRecipeSteps(ctx, runParams, ch)
			}()
			for res := range ch {
				if rec != nil {
					rec.RecordHostExecResult(res)
				}
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
		return
	}

	// ---- Synchronous path: search + execute inline, return full results. ----
	t = time.Now()
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

	secRes, _ := cuetry.NewSecretResolverWithPlugins(cuetry.SecretResolverOptionsFromHoney(api.opts.Config), pluginMgr)
	runParams := engine.CueRecipeRunParams{
		Recipe:         recipe,
		RecipeDir:      filepath.Dir(recipePath),
		Records:        searchOut.Records,
		SSHUser:        sshUser,
		CLIEnv:         envMap,
		ConfigPath:     api.opts.ConfigPath,
		AISystemPrompt: aiPrompt,
		SecretResolver: secRes,
		PluginMgr:      pluginMgr,
		Execute:        true,
		Obs:            api.metrics,
		Reg:            api.opts.ExecRegistry,
		Pools:          api.pgPools,
		Cache:          api.sshCache,
	}

	var rec *engine.SessionRecorder
	if strings.TrimSpace(api.opts.RecordDir) != "" {
		rec, err = engine.NewBatchSessionRecorder(api.opts.RecordDir, "web-webhook-"+webhookName, sshUser, len(searchOut.Records))
		if err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		hash, _ := recipe.HashJSON()
		rec.RecordRecipeMeta(engine.RecipeMeta{
			RecipePath:        recipePath,
			HostCount:         len(searchOut.Records),
			RecipeContentHash: hash,
			StartedAt:         time.Now().UTC(),
			Hosts:             engine.HostsForRecipeMeta(searchOut.Records, maxWebExecRecords),
		})
	}
	defer func() {
		if rec != nil {
			_ = rec.Close()
		}
	}()

	ch := make(chan engine.HostExecResult, cueExecChannelCap)
	errCh := make(chan error, 1)
	go func() {
		defer close(ch)
		errCh <- engine.StreamCueRecipeSteps(r.Context(), runParams, ch)
	}()

	var results []engine.HostExecResult
	for res := range ch {
		if rec != nil {
			rec.RecordHostExecResult(res)
		}
		results = append(results, res)
	}

	if streamErr := <-errCh; streamErr != nil {
		if rec != nil {
			rec.RecordError(streamErr)
		}
		httpError(w, streamErr, http.StatusInternalServerError)
		return
	}

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
