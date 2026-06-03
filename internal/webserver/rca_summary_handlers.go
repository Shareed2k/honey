package webserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/sashabaranov/go-openai"
)

type rcaRequest struct {
	AnomalyLine string   `json:"anomaly_line"`
	Context     []string `json:"context"`
	Source      string   `json:"source"`
}

type rcaResponse struct {
	Markdown string `json:"markdown"`
}

type logStats struct {
	Template string  `json:"template"`
	Count    int     `json:"count"`
	Score    float64 `json:"score"`
}

type summaryRequest struct {
	Stats []logStats `json:"stats"`
}

type summaryResponse struct {
	Markdown string `json:"markdown"`
}

func (s *Server) handleLogsRCA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req rcaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}

	endpoint, model := s.getLLMConfig()

	// Build a diagnostic prompt for the LLM
	var prompt strings.Builder
	prompt.WriteString("You are a principal systems reliability engineer (SRE) and root cause analysis (RCA) agent.\n\n")
	prompt.WriteString("Please diagnose the following anomalous log line and provide a concise Markdown report.\n\n")
	fmt.Fprintf(&prompt, "### Target Anomaly:\n`%s` (Source: %s)\n\n", req.AnomalyLine, req.Source)

	if len(req.Context) > 0 {
		prompt.WriteString("### Surrounding Log Context:\n```\n")
		for _, line := range req.Context {
			prompt.WriteString(line + "\n")
		}
		prompt.WriteString("```\n\n")
	}

	prompt.WriteString("Please format your response into three clear Markdown sections:\n")
	prompt.WriteString("1. **Root Cause Analysis**: Diagnose what likely caused this error based on the anomaly and its context.\n")
	prompt.WriteString("2. **Impact Assessment**: Explain what services or operations might be affected.\n")
	prompt.WriteString("3. **Actionable Remediation**: Provide concrete, step-by-step commands or actions to resolve the issue.")

	// Request LLM completion using our OpenAI client wrapper or direct prompt
	res, err := s.callDirectLLM(r.Context(), endpoint, model, prompt.String())
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rcaResponse{Markdown: res})
}

func (s *Server) handleLogsSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req summaryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}

	endpoint, model := s.getLLMConfig()

	var prompt strings.Builder
	prompt.WriteString("You are logSage, an advanced executive log summarization assistant.\n\n")
	prompt.WriteString("I am going to provide you with the frequency statistics of active log templates processed by our system. Please write a concise, executive system summary (under 200 words) describing the operational health of the system, calling out any anomalies or error trends.\n\n")
	prompt.WriteString("### Active Log Templates Statistics:\n")
	for _, stat := range req.Stats {
		anomalyStr := "Normal"
		if stat.Score >= 0.80 {
			anomalyStr = "Highly Suspicious / Anomaly"
		}
		fmt.Fprintf(&prompt, "- Template: `%s` | Count: %d | Status: %s (Score: %.2f)\n", stat.Template, stat.Count, anomalyStr, stat.Score)
	}

	res, err := s.callDirectLLM(r.Context(), endpoint, model, prompt.String())
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(summaryResponse{Markdown: res})
}

func (s *Server) getLLMConfig() (string, string) {
	return s.opts.Config.Defaults.Logs.AnomalyEndpoint, s.opts.Config.Defaults.Logs.AnomalyLLMModel
}

func (s *Server) callDirectLLM(ctx context.Context, endpoint, model, prompt string) (string, error) {
	var client *openai.Client
	var targetModel string

	if endpoint != "" {
		cfg := openai.DefaultConfig("")
		cfg.BaseURL = endpoint
		client = openai.NewClientWithConfig(cfg)
		targetModel = model
	} else if key := assistAPIKey(); key != "" {
		cfg := openai.DefaultConfig(key)
		if u := assistBaseURL(); u != "" {
			cfg.BaseURL = u
		}
		client = openai.NewClientWithConfig(cfg)
		targetModel = model
		if targetModel == "" {
			targetModel = "gpt-4o-mini"
		}
	} else {
		return "", errors.New("no LLM provider is configured. Please set anomaly_endpoint in config defaults or set OPENAI_API_KEY")
	}

	if targetModel == "" {
		targetModel = "gpt-4o-mini"
	}

	ctx2, cancel := context.WithTimeout(ctx, assistUpstreamTimeout())
	defer cancel()

	resp, err := client.CreateChatCompletion(ctx2, openai.ChatCompletionRequest{
		Model: targetModel,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleUser,
				Content: prompt,
			},
		},
	})
	if err != nil {
		return "", err
	}

	reply, err := assistExtractAssistantReply(resp)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(reply), nil
}
