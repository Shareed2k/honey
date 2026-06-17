// Package alertwebhook implements an Alertmanager-compatible webhook receiver.
// It receives alert payloads, deduplicates them with an LRU cache, resolves
// matching hosts from honey's inventory, runs investigation commands via SSH,
// and notifies configured channels with the findings.
package alertwebhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/shareed2k/honey/internal/engine"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/prometheus/alertmanager/notify/webhook"

	amtemplate "github.com/prometheus/alertmanager/template"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostapi"
	"github.com/shareed2k/honey/internal/hosts"
	"go.uber.org/zap"
)

const (
	defaultPort          = 9095
	defaultDedupWindow   = time.Hour
	defaultDedupCapacity = 10_000
)

// Server is the Alertmanager webhook receiver.
type Server struct {
	cfg         config.AlertWebhookConfig
	fileCfg     *config.File
	cfgPath     string
	seen        *lru.Cache[string, time.Time]
	dedupWindow time.Duration
}

// DefaultConfig returns an AlertWebhookConfig with sensible defaults.
func DefaultConfig() config.AlertWebhookConfig {
	return config.AlertWebhookConfig{Port: defaultPort, DedupWindow: "1h", DedupCapacity: defaultDedupCapacity}
}

// New creates a new webhook server from the honey config.
func New(cfg config.AlertWebhookConfig, fileCfg *config.File, cfgPath string) (*Server, error) {
	capacity := cfg.DedupCapacity
	if capacity <= 0 {
		capacity = defaultDedupCapacity
	}
	cache, err := lru.New[string, time.Time](capacity)
	if err != nil {
		return nil, fmt.Errorf("dedup cache: %w", err)
	}
	window := defaultDedupWindow
	if s := strings.TrimSpace(cfg.DedupWindow); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return nil, fmt.Errorf("dedup_window: %w", err)
		}
		window = d
	}
	return &Server{
		cfg:         cfg,
		fileCfg:     fileCfg,
		cfgPath:     cfgPath,
		seen:        cache,
		dedupWindow: window,
	}, nil
}

// ListenAndServe starts the HTTP server and blocks until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	port := s.cfg.Port
	if port <= 0 {
		port = defaultPort
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/webhook/alert", s.handleAlert)
	mux.HandleFunc("/webhook/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	fmt.Printf("honey alert webhook listening on :%d\n", port)
	err := srv.ListenAndServe()
	wg.Wait()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *Server) handleAlert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if tok := strings.TrimSpace(s.cfg.Token); tok != "" {
		auth := r.Header.Get("Authorization")
		if !strings.EqualFold(strings.TrimPrefix(auth, "Bearer "), tok) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	var msg webhook.Message
	if err := json.Unmarshal(body, &msg); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Respond immediately — investigation runs async.
	w.WriteHeader(http.StatusOK)

	if msg.Data == nil {
		return
	}
	for i := range msg.Alerts {
		alert := msg.Alerts[i]
		if alert.Status != "firing" {
			continue
		}
		if s.isDuplicate(alert.Fingerprint) {
			continue
		}
		go s.investigate(r.Context(), alert)
	}
}

func (s *Server) isDuplicate(fingerprint string) bool {
	if fingerprint == "" {
		return false
	}
	if t, ok := s.seen.Get(fingerprint); ok && time.Since(t) < s.dedupWindow {
		return true
	}
	s.seen.Add(fingerprint, time.Now())
	return false
}

func (s *Server) investigate(ctx context.Context, alert amtemplate.Alert) {
	mapping := resolveMapping(s.fileCfg, alert.Labels)
	if mapping == nil {
		zap.L().Warn("alert webhook: no mapping for alert", zap.Any("labels", alert.Labels))
		return
	}

	hostQuery, err := evalHostQuery(mapping.HostQuery, alert.Labels)
	if err != nil || hostQuery == "" {
		zap.L().Warn("alert webhook: host_query failed", zap.Any("labels", alert.Labels), zap.Error(err))
		return
	}

	records, err := hostapi.SearchHosts(ctx, &hostapi.SearchHostsInput{Name: hostQuery}, nil, nil)
	if err != nil || len(records.Records) == 0 {
		zap.L().Warn("alert webhook: no hosts found", zap.String("host_query", hostQuery), zap.Error(err))
		return
	}

	if !s.cfg.AutoInvestigate || strings.TrimSpace(mapping.Command) == "" {
		zap.L().Info("alert webhook: matched hosts, no command configured", zap.String("host_query", hostQuery))
		return
	}

	cmd := mapping.Command
	recordDir := ""
	if s.fileCfg != nil {
		recordDir = strings.TrimSpace(s.fileCfg.Defaults.RecordDir)
	}

	results, _ := engine.ExecuteSSHParallel("", records.Records, func(_ hosts.Record) string { return cmd }, 8, nil)
	// Build notification body.
	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "Alert: %s\nHost query: %s\n\n", alert.Labels["alertname"], hostQuery)
	for _, res := range results {
		_, _ = fmt.Fprintf(&sb, "--- %s (%s) exit=%d ---\n%s\n", res.Name, res.IP, res.ExitCode, res.Output)
		if recordDir != "" {
			// Recording already captured by ExecuteSSHParallel if record_dir set via env;
			// noting here for completeness.
			_ = recordDir
		}
	}
	body := sb.String()
	fmt.Print(body)

	if mapping.Notify != nil {
		sendNotifications(ctx, mapping.Notify, body, alert.Labels)
	}
}

func resolveMapping(cfg *config.File, labels amtemplate.KV) *config.AlertMapping {
	if cfg == nil {
		return nil
	}
	for i := range cfg.AlertMappings {
		m := &cfg.AlertMappings[i]
		match := true
		for k, v := range m.MatchLabels {
			if labels[k] != v {
				match = false
				break
			}
		}
		if match {
			return m
		}
	}
	return nil
}

func evalHostQuery(hostQuery string, labels amtemplate.KV) (string, error) {
	tmpl, err := template.New("").Option("missingkey=zero").Parse(hostQuery)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]string(labels)); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}

func sendNotifications(ctx context.Context, notify *config.AlertNotify, body string, labels amtemplate.KV) {
	subject := notify.Subject
	if subject == "" {
		subject = "honey alert findings: " + labels["alertname"]
	}

	if notify.HTTP != nil {
		sendHTTP(ctx, notify.HTTP, subject, body)
	}
	if notify.Slack != nil {
		sendSlack(ctx, notify.Slack, subject, body)
	}
	if notify.Telegram != nil {
		sendTelegram(ctx, notify.Telegram, subject, body)
	}
}

func sendHTTP(ctx context.Context, cfg *config.AlertNotifyHTTP, subject, body string) {
	payload, _ := json.Marshal(map[string]string{"subject": subject, "body": body})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(payload))
	if err != nil {
		zap.L().Error("alert notify HTTP: build request failed", zap.Error(err))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		zap.L().Error("alert notify HTTP: request failed", zap.Error(err))
		return
	}
	defer resp.Body.Close()
}

func sendSlack(ctx context.Context, cfg *config.AlertNotifySlack, subject, body string) {
	text := "*" + subject + "*\n```" + body + "```"
	payload := map[string]string{"text": text}
	if cfg.ChannelID != "" {
		payload["channel"] = cfg.ChannelID
	}
	data, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.WebhookURL, bytes.NewReader(data))
	if err != nil {
		zap.L().Error("alert notify Slack: build request failed", zap.Error(err))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		zap.L().Error("alert notify Slack: request failed", zap.Error(err))
		return
	}
	defer resp.Body.Close()
}

func sendTelegram(ctx context.Context, cfg *config.AlertNotifyTelegram, subject, body string) {
	botToken := strings.TrimSpace(cfg.BotToken)
	if botToken == "" {
		return
	}
	text := subject + "\n\n" + body
	for _, chatID := range cfg.ChatIDs {
		url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)
		payload, _ := json.Marshal(map[string]string{"chat_id": chatID, "text": text})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			zap.L().Error("alert notify Telegram: request failed", zap.Error(err))
			continue
		}
		resp.Body.Close()
	}
}
