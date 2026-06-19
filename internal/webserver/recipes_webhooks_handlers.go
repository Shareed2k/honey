package webserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

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
func (s *Server) handleRecipeWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	appName := r.PathValue("app_name")
	webhookName := r.PathValue("webhook_name")

	if s.opts.Config == nil || s.opts.Config.Apps == nil {
		httpError(w, fmt.Errorf("no apps configured"), http.StatusNotFound)
		return
	}

	app, ok := s.opts.Config.Apps[appName]
	if !ok || app.Type != apps.AppTypeRecipe || strings.TrimSpace(app.TargetRecipe) == "" {
		httpError(w, fmt.Errorf("app not found or not a valid recipe app"), http.StatusNotFound)
		return
	}

	// 1. Resolve and parse recipe
	recipePath := strings.TrimSpace(app.TargetRecipe)
	if !filepath.IsAbs(recipePath) && s.opts.ConfigPath != "" {
		recipePath = filepath.Join(filepath.Dir(s.opts.ConfigPath), recipePath)
	}

	raw, err := safepath.ReadFile(recipePath)
	if err != nil {
		httpError(w, fmt.Errorf("read target recipe: %w", err), http.StatusBadRequest)
		return
	}

	pluginMgr, err := plugins.Open(r.Context(), s.opts.Config)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	defer func() { _ = pluginMgr.Close() }()

	recipe, err := cuetry.ParseRemoteRecipeOpts(raw, nil, cuetry.ParseOptions{PluginManager: pluginMgr})
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
		secRes, err := cuetry.NewSecretResolverWithPlugins(cuetry.SecretResolverOptionsFromHoney(s.opts.Config), pluginMgr)
		if err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		expected, err := secRes.Resolve(r.Context(), authSecret)
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

	// 4. Resolve Target Hosts
	target := app.Target
	if target == "" && app.TargetRegex != "" {
		target = app.TargetRegex // Not ideal, but search supports both roughly or SearchHosts handles it.
		// Wait, hostapi.SearchHosts takes a struct. Let's build SearchHostsInput.
	}

	searchIn := &hostapi.SearchHostsInput{
		ConfigPath: s.opts.ConfigPath,
		Name:       target,
		Providers:  app.Provider,
	}
	if target == "" && app.TargetRegex != "" {
		searchIn.NameRegex = app.TargetRegex
	}

	searchOut, err := hostapi.SearchHosts(r.Context(), searchIn, s.opts.ExecRegistry, s.opts.SearchRegistry)
	if err != nil {
		httpError(w, fmt.Errorf("search hosts: %w", err), http.StatusBadRequest)
		return
	}

	if len(searchOut.Records) == 0 {
		httpError(w, fmt.Errorf("no target hosts found for app %q", appName), http.StatusBadRequest)
		return
	}

	// 5. Prepare Run Parameters
	secRes, _ := cuetry.NewSecretResolverWithPlugins(cuetry.SecretResolverOptionsFromHoney(s.opts.Config), pluginMgr)
	aiPrompt := ui.LoadAISystemPromptFromConfigPath(s.opts.ConfigPath)

	runParams := engine.CueRecipeRunParams{
		Recipe:         recipe,
		RecipeDir:      filepath.Dir(recipePath),
		Records:        searchOut.Records,
		SSHUser:        s.sshUser(""), // Or default
		CLIEnv:         envMap,
		ConfigPath:     s.opts.ConfigPath,
		AISystemPrompt: aiPrompt,
		SecretResolver: secRes,
		PluginMgr:      pluginMgr,
		Execute:        true,
		Obs:            s.metrics,
		Reg:            s.opts.ExecRegistry,
		Pools:          s.pgPools,
	}

	// Session Recorder
	var rec *engine.SessionRecorder
	wantRec := strings.TrimSpace(s.opts.RecordDir) != ""
	if wantRec {
		rec, err = engine.NewBatchSessionRecorder(s.opts.RecordDir, "web-webhook-"+webhookName, runParams.SSHUser, len(searchOut.Records))
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

	isAsync := webhook.Async != nil && *webhook.Async

	if isAsync {
		if s.webhookQueue == nil {
			if rec != nil {
				rec.RecordError(fmt.Errorf("webhook queue not initialized"))
				_ = rec.Close()
			}
			httpError(w, fmt.Errorf("server queue not configured"), http.StatusInternalServerError)
			return
		}

		err := s.webhookQueue.Submit(func() {
			defer func() {
				if rec != nil {
					_ = rec.Close()
				}
			}()

			ch := make(chan engine.HostExecResult, cueExecChannelCap)
			go func() {
				defer close(ch)
				_ = engine.StreamCueRecipeSteps(context.Background(), runParams, ch)
			}()

			for res := range ch {
				if rec != nil {
					rec.RecordHostExecResult(res)
				}
			}
		})
		if err != nil {
			if err == queue.ErrQueueFull {
				httpError(w, fmt.Errorf("server busy: webhook queue full"), http.StatusTooManyRequests)
				return
			}
			httpError(w, fmt.Errorf("submit webhook task: %w", err), http.StatusInternalServerError)
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

	// Synchronous execution
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
func (s *Server) handleRecipeWebhookResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpError(w, fmt.Errorf("missing id"), http.StatusBadRequest)
		return
	}

	if s.opts.RecordDir == "" {
		httpError(w, fmt.Errorf("session recording is not enabled on this server"), http.StatusNotImplemented)
		return
	}

	events, err := recordings.LoadEvents(s.opts.RecordDir, id+".hrec.jsonl")
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
