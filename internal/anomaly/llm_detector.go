package anomaly

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	openai "github.com/sashabaranov/go-openai"
)

type llmDetector struct {
	model        string
	threshold    float64
	client       *openai.Client
	contextLines int
	mu           sync.Mutex
	recent       []string
}

func newLLMDetector(endpoint, model string, threshold float64, contextLines int) *llmDetector {
	if strings.TrimSpace(model) == "" {
		model = "llama3"
	}
	if contextLines < 0 {
		contextLines = 0
	}
	cfg := openai.DefaultConfig("ollama") // API key unused by Ollama/LM Studio
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	return &llmDetector{
		model:        model,
		threshold:    threshold,
		client:       openai.NewClientWithConfig(cfg),
		contextLines: contextLines,
	}
}

const llmSystemPrompt = `You are an expert system log analyst.

Examples:
User: "info server started on port <num>"
Assistant: {"anomaly":false,"score":0.04,"reason":"routine startup message"}

User: "error authentication failed for user root from <ip>"
Assistant: {"anomaly":true,"score":0.96,"reason":"authentication failure with privileged account"}

Analyze the log line (or sequence) below and decide if it indicates an anomaly (error, failure, crash, security issue, or unexpected behavior). When recent context lines are provided, consider them as a sequence. Respond ONLY with valid JSON: {"anomaly":true,"score":0.95,"reason":"brief explanation"} where score is 0.0–1.0.`

type llmResult struct {
	Anomaly bool    `json:"anomaly"`
	Score   float64 `json:"score"`
	Reason  string  `json:"reason"`
}

func (d *llmDetector) Score(ctx context.Context, line string) (Result, error) {
	n := normalize(line)
	if n == "" {
		return Result{Score: 0, Anomaly: false, Reason: "empty", Original: line}, nil
	}

	userMsg := d.buildUserMessage(n)

	resp, err := d.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: d.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: llmSystemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: userMsg},
		},
	})
	if err != nil {
		return Result{}, fmt.Errorf("llm request: %w", err)
	}
	if len(resp.Choices) == 0 {
		return Result{}, fmt.Errorf("llm returned no choices")
	}

	content := resp.Choices[0].Message.Content
	// strip markdown fences if the model wraps its JSON output
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start >= 0 && end > start {
		content = content[start : end+1]
	}

	var r llmResult
	if err := json.Unmarshal([]byte(content), &r); err != nil {
		return Result{}, fmt.Errorf("llm result parse: %w", err)
	}

	r.Score = clamp01(r.Score)
	return Result{
		Score:    r.Score,
		Anomaly:  r.Score >= d.threshold,
		Reason:   "llm:" + r.Reason,
		Original: line,
	}, nil
}

func (d *llmDetector) buildUserMessage(normalizedLine string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.recent = append(d.recent, normalizedLine)
	if len(d.recent) > d.contextLines+1 {
		d.recent = d.recent[len(d.recent)-(d.contextLines+1):]
	}
	if d.contextLines == 0 || len(d.recent) <= 1 {
		return normalizedLine
	}
	ctx := d.recent[:len(d.recent)-1]
	var sb strings.Builder
	sb.WriteString("Recent context:\n")
	for _, l := range ctx {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	sb.WriteString("\nCurrent line: ")
	sb.WriteString(normalizedLine)
	return sb.String()
}
