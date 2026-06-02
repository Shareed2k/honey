package anomaly

import (
	"context"
	"fmt"
	"strings"
	"sync"

	lru "github.com/hashicorp/golang-lru/v2"
	openai "github.com/sashabaranov/go-openai"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// llmCacheMaxSize is the maximum number of normalized-line → Result entries
// kept in the LRU cache. Entries are evicted in least-recently-used order.
const llmCacheMaxSize = 10_000

var llmResultSchema *jsonschema.Definition

func init() {
	var r llmResult
	s, err := jsonschema.GenerateSchemaForType(r)
	if err != nil {
		panic("anomaly: failed to generate llmResult JSON schema: " + err.Error())
	}
	llmResultSchema = s
}

type llmDetector struct {
	model        string
	threshold    float64
	client       *openai.Client
	contextLines int
	mu           sync.Mutex
	recent       []string
	cache        *lru.Cache[string, Result]
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
	c, _ := lru.New[string, Result](llmCacheMaxSize) // error only on size <= 0, which can't happen here
	return &llmDetector{
		model:        model,
		threshold:    threshold,
		client:       openai.NewClientWithConfig(cfg),
		contextLines: contextLines,
		cache:        c,
	}
}

// NewLLMDetector exposes the internal newLLMDetector function for public use.
func NewLLMDetector(endpoint, model string, threshold float64, contextLines int) Detector {
	return newLLMDetector(endpoint, model, threshold, contextLines)
}

const llmSystemPrompt = `You are an expert system log analyst.

Examples:
User: "info server started on port <num>"
Assistant: {"anomaly":false,"score":0.04,"reason":"routine startup message"}

User: "error authentication failed for user root from <ip>"
Assistant: {"anomaly":true,"score":0.96,"reason":"authentication failure with privileged account"}

Analyze the log line (or sequence) below and decide if it indicates an anomaly (error, failure, crash, security issue, or unexpected behavior). When recent context lines are provided, consider them as a sequence. Return anomaly status, a score between 0.0 and 1.0, and a brief reason.`

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

	// Cache is only keyed on the normalized line when there is no context window,
	// because with context the same line produces different user messages.
	if d.contextLines == 0 {
		if r, ok := d.cache.Get(n); ok {
			r.Original = line
			return r, nil
		}
	}

	userMsg := d.buildUserMessage(n)

	resp, err := d.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: d.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: d.buildSystemPrompt(n)},
			{Role: openai.ChatMessageRoleUser, Content: userMsg},
		},
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONSchema,
			JSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
				Name:   "log_anomaly",
				Schema: llmResultSchema,
				Strict: true,
			},
		},
	})
	if err != nil {
		return Result{}, fmt.Errorf("llm request: %w", err)
	}
	if len(resp.Choices) == 0 {
		return Result{}, fmt.Errorf("llm returned no choices")
	}

	var r llmResult
	if err := llmResultSchema.Unmarshal(resp.Choices[0].Message.Content, &r); err != nil {
		return Result{}, fmt.Errorf("llm result parse: %w", err)
	}

	r.Score = clamp01(r.Score)
	result := Result{
		Score:    r.Score,
		Anomaly:  r.Score >= d.threshold,
		Reason:   "llm:" + r.Reason,
		Original: line,
	}
	if d.contextLines == 0 {
		d.cache.Add(n, result)
	}
	return result, nil
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

func (d *llmDetector) buildSystemPrompt(normalizedLine string) string {
	tokens := tokenize(normalizedLine)
	demos := SelectDefaultDemonstrations(tokens, 2)
	if len(demos) == 0 {
		return llmSystemPrompt
	}

	var sb strings.Builder
	sb.WriteString("You are an expert system log analyst.\n\n")
	sb.WriteString("Here are contextually relevant examples of logs and their correct classification:\n\n")

	for i, demo := range demos {
		scoreStr := fmt.Sprintf("%g", demo.Score)
		if !strings.Contains(scoreStr, ".") {
			scoreStr += ".0"
		}
		fmt.Fprintf(&sb, "User: %q\n", demo.Template)
		fmt.Fprintf(&sb, "Assistant: {\"anomaly\":%t,\"score\":%s,\"reason\":%q}\n", demo.Anomaly, scoreStr, demo.Reason)
		if i < len(demos)-1 {
			sb.WriteString("\n")
		}
	}

	sb.WriteString("\n")
	sb.WriteString("Analyze the log line (or sequence) below and decide if it indicates an anomaly (error, failure, crash, security issue, or unexpected behavior). When recent context lines are provided, consider them as a sequence. Return anomaly status, a score between 0.0 and 1.0, and a brief reason.")
	return sb.String()
}
