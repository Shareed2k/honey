package webserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"
	"go.uber.org/zap"
)

const (
	assistModelsCacheTTL  = 3 * time.Minute
	assistListModelsSec   = 30
	maxAssistModelsReply  = 500
	maxAssistModelIDRunes = 128
)

func assistNewOpenAIClient() *openai.Client {
	key := assistAPIKey()
	cfg := openai.DefaultConfig(key)
	if u := assistBaseURL(); u != "" {
		cfg.BaseURL = u
	}
	return openai.NewClientWithConfig(cfg)
}

func modelIDsSortedFromList(list openai.ModelsList) []string {
	set := make(map[string]struct{}, len(list.Models))
	for _, m := range list.Models {
		id := strings.TrimSpace(m.ID)
		if id != "" {
			set[id] = struct{}{}
		}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) > maxAssistModelsReply {
		ids = ids[:maxAssistModelsReply]
	}
	return ids
}

func (s *Server) pullAssistModelsFromUpstream(ctx context.Context) ([]string, error) {
	client := assistNewOpenAIClient()
	zap.L().Debug(
		"terminal assist ListModels call",
		zap.Bool("openai_base_url_configured", assistBaseURL() != ""),
	)
	list, err := client.ListModels(ctx)
	if err != nil {
		zap.L().Debug("terminal assist ListModels failed", zap.Error(err))
		return nil, err
	}
	ids := modelIDsSortedFromList(list)
	zap.L().Debug(
		"terminal assist ListModels ok",
		zap.Int("raw_models", len(list.Models)),
		zap.Int("deduped_ids", len(ids)),
	)
	return ids, nil
}

// getAssistModelIDs returns model ids from cache or refreshes from the provider.
// On upstream error, returns a stale cached slice when available and a non-nil err.
func (s *Server) getAssistModelIDs(ctx context.Context, force bool) ([]string, error) {
	s.assistModelsMu.Lock()
	stale := append([]string(nil), s.assistModelIDs...)
	expired := force || len(s.assistModelIDs) == 0 || time.Now().After(s.assistModelsExp)
	s.assistModelsMu.Unlock()

	if !expired && len(stale) > 0 {
		zap.L().Debug(
			"terminal assist model list cache hit",
			zap.Int("count", len(stale)),
			zap.Bool("force", force),
		)
		return stale, nil
	}

	zap.L().Debug(
		"terminal assist model list cache miss or refresh",
		zap.Bool("force", force),
		zap.Bool("expired", expired),
		zap.Int("stale_count", len(stale)),
	)

	ids, err := s.pullAssistModelsFromUpstream(ctx)
	if err != nil {
		if len(stale) > 0 {
			zap.L().Debug(
				"terminal assist model list using stale after upstream error",
				zap.Error(err),
				zap.Int("stale_count", len(stale)),
			)
			return stale, err
		}
		zap.L().Debug("terminal assist model list upstream error no cache", zap.Error(err))
		return nil, err
	}

	s.assistModelsMu.Lock()
	s.assistModelIDs = append([]string(nil), ids...)
	s.assistModelsExp = time.Now().Add(assistModelsCacheTTL)
	s.assistModelsMu.Unlock()
	zap.L().Debug(
		"terminal assist model list cache updated",
		zap.Int("count", len(ids)),
		zap.Duration("ttl", assistModelsCacheTTL),
	)
	return ids, nil
}

// handleTerminalAssistModels lists OpenAI-compatible model IDs for terminal assist.
// @Summary Terminal assist models
// @Tags assist
// @Produce json
// @Success 200 {object} TerminalAssistModelsResponse
// @Failure 503 {object} map[string]string
// @Router /api/v1/terminal-assist/models [get]
// @Security BearerAuth
func (s *Server) handleTerminalAssistModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if assistAPIKey() == "" {
		httpError(w, errors.New("terminal assist is not configured (set OPENAI_API_KEY)"), http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(assistListModelsSec)*time.Second)
	defer cancel()
	ids, err := s.getAssistModelIDs(ctx, false)
	if err != nil && len(ids) == 0 {
		zap.L().Debug("terminal assist GET models failed no cache", zap.Error(err))
		httpError(w, fmt.Errorf("list models: %w", err), http.StatusBadGateway)
		return
	}
	if err != nil {
		zap.L().Warn("terminal assist list models failed, returning stale cache", zap.Error(err))
	}
	zap.L().Debug(
		"terminal assist GET models response",
		zap.Int("returned_count", len(ids)),
		zap.Bool("stale_with_error", err != nil),
	)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(TerminalAssistModelsResponse{Models: ids})
}

func modelIDInList(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// resolveAssistChatModel returns the model id for CreateChatCompletion.
// requested must be non-empty and must appear in the provider ListModels result (cached or after one forced refresh).
func (s *Server) resolveAssistChatModel(ctx context.Context, requested string) (string, error) {
	req := strings.TrimSpace(requested)
	if req == "" {
		return "", errors.New("model is required: choose a model from GET /api/v1/terminal-assist/models")
	}
	if len([]rune(req)) > maxAssistModelIDRunes {
		zap.L().Debug("terminal assist resolve model too long", zap.Int("max_runes", maxAssistModelIDRunes))
		return "", fmt.Errorf("model id too long (max %d characters)", maxAssistModelIDRunes)
	}
	ids, _ := s.getAssistModelIDs(ctx, false)
	if modelIDInList(ids, req) {
		zap.L().Debug("terminal assist resolve model matched cache", zap.String("model", req), zap.Int("list_size", len(ids)))
		return req, nil
	}
	ids, err := s.getAssistModelIDs(ctx, true)
	if err == nil && modelIDInList(ids, req) {
		zap.L().Debug("terminal assist resolve model matched after refresh", zap.String("model", req), zap.Int("list_size", len(ids)))
		return req, nil
	}
	if len(ids) == 0 {
		return "", errors.New("no models available from provider: fix ListModels / OPENAI_BASE_URL, then reload the terminal")
	}
	zap.L().Debug(
		"terminal assist resolve model rejected",
		zap.String("requested", req),
		zap.Int("list_size", len(ids)),
		zap.Bool("upstream_refresh_err", err != nil),
	)
	return "", errors.New("unknown model: pick an id returned by GET /api/v1/terminal-assist/models")
}
