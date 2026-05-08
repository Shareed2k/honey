package webserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/sashabaranov/go-openai"
	"go.uber.org/zap"
)

const (
	defaultAssistMaxScrollbackRunes = 24000
	defaultAssistMaxUserRunes       = 4000
	defaultAssistRPM                = 30
	defaultAssistMaxTokens          = 1024
	defaultAssistUpstreamSec        = 90
	maxAssistRequestBody            = 512 << 10 // 512 KiB
	maxAssistLinesFromClient        = 500
)

type terminalAssistRequest struct {
	UserPrompt string `json:"user_prompt"`
	Scrollback string `json:"scrollback"`
	MaxLines   int    `json:"max_lines"`
	Model      string `json:"model"`
}

type terminalAssistResponse struct {
	Reply            string `json:"reply"`
	ScrollbackClipped bool  `json:"scrollback_clipped"`
}

func assistAPIKey() string {
	return strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
}

func assistBaseURL() string {
	return strings.TrimSpace(strings.TrimRight(os.Getenv("OPENAI_BASE_URL"), "/"))
}

func envInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def
	}
	return n
}

func assistMaxScrollbackRunes() int {
	n := envInt("TERMINAL_ASSIST_MAX_SCROLLBACK_RUNES", defaultAssistMaxScrollbackRunes)
	if n == 0 {
		return defaultAssistMaxScrollbackRunes
	}
	return n
}

func assistMaxUserRunes() int {
	n := envInt("TERMINAL_ASSIST_MAX_USER_RUNES", defaultAssistMaxUserRunes)
	if n == 0 {
		return defaultAssistMaxUserRunes
	}
	return n
}

func assistRPM() int {
	n := envInt("TERMINAL_ASSIST_RPM", defaultAssistRPM)
	if n <= 0 {
		return defaultAssistRPM
	}
	return n
}

func assistMaxTokens() int {
	n := envInt("TERMINAL_ASSIST_MAX_TOKENS", defaultAssistMaxTokens)
	if n <= 0 {
		return defaultAssistMaxTokens
	}
	return n
}

func assistUpstreamTimeout() time.Duration {
	n := envInt("TERMINAL_ASSIST_UPSTREAM_SEC", defaultAssistUpstreamSec)
	if n <= 0 {
		n = defaultAssistUpstreamSec
	}
	return time.Duration(n) * time.Second
}

func terminalAssistConfigured() bool {
	return assistAPIKey() != ""
}

// slidingRL is a fixed-window rate limiter keyed by string (e.g. client IP).
type slidingRL struct {
	mu sync.Mutex
	m  map[string][]time.Time
}

func newSlidingRL() *slidingRL {
	return &slidingRL{m: make(map[string][]time.Time)}
}

func (r *slidingRL) allow(key string, max int, window time.Duration) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-window)
	times := r.m[key]
	i := 0
	for i < len(times) && times[i].Before(cutoff) {
		i++
	}
	times = times[i:]
	if len(times) >= max {
		r.m[key] = times
		zap.L().Debug("terminal assist rate limit window full", zap.String("key", key), zap.Int("max", max))
		return false
	}
	times = append(times, now)
	r.m[key] = times
	if len(r.m) > 4096 {
		// crude bound: drop map periodically under pathological keys
		r.m = map[string][]time.Time{key: times}
	}
	return true
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func clipScrollbackByLines(s string, maxLines int) (string, bool) {
	if maxLines <= 0 || maxLines > maxAssistLinesFromClient {
		maxLines = maxAssistLinesFromClient
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= maxLines {
		return s, false
	}
	return strings.Join(lines[len(lines)-maxLines:], "\n"), true
}

func clipRunesTail(s string, maxRunes int) (string, bool) {
	if maxRunes <= 0 {
		return "", true
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s, false
	}
	return string(r[len(r)-maxRunes:]), true
}

func clipRunesHead(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes])
}

// assistExtractAssistantReply returns user-visible text from the first chat choice.
// Some gateways or reasoning models put text in reasoning_content or content[] instead of content string.
func assistExtractAssistantReply(resp openai.ChatCompletionResponse) (string, error) {
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("empty response from model: no choices (response_id=%q response_model=%q)", resp.ID, resp.Model)
	}
	ch := resp.Choices[0]
	msg := ch.Message

	if s := strings.TrimSpace(msg.Content); s != "" {
		return s, nil
	}
	var multi strings.Builder
	for _, p := range msg.MultiContent {
		if p.Type == openai.ChatMessagePartTypeText && strings.TrimSpace(p.Text) != "" {
			if multi.Len() > 0 {
				multi.WriteString("\n")
			}
			multi.WriteString(strings.TrimSpace(p.Text))
		}
	}
	if s := strings.TrimSpace(multi.String()); s != "" {
		return s, nil
	}
	if s := strings.TrimSpace(msg.ReasoningContent); s != "" {
		return s, nil
	}
	if s := strings.TrimSpace(msg.Refusal); s != "" {
		return "", fmt.Errorf("model refused: %s", s)
	}
	if len(msg.ToolCalls) > 0 {
		return "", errors.New("model returned tool calls instead of a text reply; try another model")
	}
	return "", fmt.Errorf("empty reply from model (finish_reason=%q)", ch.FinishReason)
}

const assistSystemPrompt = `You are a DevOps assistant helping someone who is using an SSH terminal session in a web UI.
They may paste recent terminal output (scrollback) and a short question.
Rules:
- Prefer actionable shell commands, explanations of errors, or next diagnostic steps.
- Never invent secret values, passwords, or tokens; if credentials might be in the paste, warn briefly and suggest rotation.
- Keep answers short unless the user explicitly asks for detail.
- Do not repeat the entire scrollback back unless needed.`

func (s *Server) handleTerminalAssist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	key := assistAPIKey()
	if key == "" {
		httpError(w, errors.New("terminal assist is not configured (set OPENAI_API_KEY)"), http.StatusServiceUnavailable)
		return
	}

	var req terminalAssistRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, maxAssistRequestBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		zap.L().Debug("terminal assist decode failed", zap.Error(err))
		httpError(w, fmt.Errorf("invalid JSON: %w", err), http.StatusBadRequest)
		return
	}
	scroll := strings.TrimSpace(req.Scrollback)
	if scroll == "" {
		zap.L().Debug("terminal assist rejected empty scrollback")
		httpError(w, errors.New("scrollback is required"), http.StatusBadRequest)
		return
	}
	zap.L().Debug("terminal assist request",
		zap.String("client_ip", clientIP(r)),
		zap.Int("max_lines", req.MaxLines),
		zap.Bool("model_field_set", strings.TrimSpace(req.Model) != ""),
		zap.Int("scrollback_runes_in", utf8.RuneCountInString(scroll)),
		zap.Int("user_prompt_runes_in", utf8.RuneCountInString(strings.TrimSpace(req.UserPrompt))),
	)
	resolveCtx, resolveCancel := context.WithTimeout(r.Context(), 25*time.Second)
	chatModel, err := s.resolveAssistChatModel(resolveCtx, req.Model)
	resolveCancel()
	if err != nil {
		zap.L().Debug("terminal assist model resolve failed", zap.Error(err))
		httpError(w, err, http.StatusBadRequest)
		return
	}
	zap.L().Debug("terminal assist model resolved", zap.String("chat_model", chatModel))
	if !s.assistRL.allow(clientIP(r), assistRPM(), time.Minute) {
		zap.L().Debug("terminal assist rate limited", zap.String("client_ip", clientIP(r)))
		httpError(w, errors.New("rate limit exceeded; try again in a minute"), http.StatusTooManyRequests)
		return
	}
	clippedLines := false
	if req.MaxLines > 0 {
		var c bool
		scroll, c = clipScrollbackByLines(scroll, req.MaxLines)
		clippedLines = clippedLines || c
	}
	maxSR := assistMaxScrollbackRunes()
	var clippedRunes bool
	scroll, clippedRunes = clipRunesTail(scroll, maxSR)
	user := clipRunesHead(strings.TrimSpace(req.UserPrompt), assistMaxUserRunes())
	if user == "" {
		user = "Briefly suggest the next command or explain the latest output."
	}

	zap.L().Debug("terminal assist context after clip",
		zap.Int("scrollback_runes", utf8.RuneCountInString(scroll)),
		zap.Int("scrollback_lines", strings.Count(scroll, "\n")+1),
		zap.Bool("clipped_lines", clippedLines),
		zap.Bool("clipped_runes", clippedRunes),
		zap.Bool("user_prompt_defaulted", strings.TrimSpace(req.UserPrompt) == ""),
	)

	userContent := fmt.Sprintf("User question:\n%s\n\n--- Terminal scrollback (tail) ---\n%s", user, scroll)
	zap.L().Debug("terminal assist calling CreateChatCompletion",
		zap.String("model", chatModel),
		zap.Int("max_tokens", assistMaxTokens()),
		zap.Duration("timeout", assistUpstreamTimeout()),
		zap.Int("user_message_runes", utf8.RuneCountInString(userContent)),
	)

	reply, err := assistCreateChatCompletion(r.Context(), chatModel, assistSystemPrompt, userContent)
	if err != nil {
		zap.L().Warn("terminal assist upstream error", zap.Error(err))
		httpError(w, fmt.Errorf("upstream error: %v", err), http.StatusBadGateway)
		return
	}
	zap.L().Debug("terminal assist completion ok",
		zap.String("model", chatModel),
		zap.Int("reply_runes", utf8.RuneCountInString(reply)),
		zap.Bool("scrollback_clipped_response", clippedLines || clippedRunes),
	)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(terminalAssistResponse{
		Reply:             reply,
		ScrollbackClipped: clippedLines || clippedRunes,
	})
}
