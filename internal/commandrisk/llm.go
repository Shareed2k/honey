package commandrisk

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// LLMAdvice is an advisory risk classification from a model. It augments the
// deterministic Analysis for explanation/UX only — it is NEVER used in any
// allow/deny decision. The engine order is: built-in critical → OPA → (advice).
type LLMAdvice struct {
	Risk        Severity `json:"risk"`
	Reasons     []string `json:"reasons"`
	Explanation string   `json:"explanation,omitempty"`
}

// Advisor produces an advisory classification for a command. Implementations
// must be best-effort: any failure returns (nil, err) and never blocks a run.
// This is the pluggable seam — an LLM today, a local ONNX/fastText classifier or
// a honey-trained command-risk model later — without touching callers.
//
// A future trained classifier would map (command, context) to labels such as:
// read_only, deletes_files, privilege_escalation, network_download_execute,
// service_restart, cloud_destructive, kubernetes_destructive, prod_sensitive.
type Advisor interface {
	Advise(ctx context.Context, command string, detected Detected) (*LLMAdvice, error)
}

// CompleteFunc is the model-completion dependency (matches aichat.Complete). It
// is injected so this package stays free of any specific LLM client and is
// trivially testable with a stub.
type CompleteFunc func(ctx context.Context, system, user, model string, maxTokens int) (string, error)

// LLMAdvisor classifies command risk via a chat-completion model (e.g. a local
// Ollama/llama.cpp endpoint through aichat). It is authoritative for nothing.
type LLMAdvisor struct {
	complete CompleteFunc
	model    string
}

// NewLLMAdvisor builds an advisor over the given completion func and model.
func NewLLMAdvisor(complete CompleteFunc, model string) *LLMAdvisor {
	return &LLMAdvisor{complete: complete, model: model}
}

const advisorSystemPrompt = `You classify the risk of a single shell command for an operations tool.
Return ONLY a JSON object: {"risk":"low|medium|high|critical","reasons":["..."],"explanation":"..."}.
You must NOT decide whether to allow or deny — only classify and explain.`

// Advise asks the model to classify the command. Parsing is defensive: a missing
// or malformed response yields (nil, err) and must never affect any decision.
func (a *LLMAdvisor) Advise(ctx context.Context, command string, detected Detected) (*LLMAdvice, error) {
	if a == nil || a.complete == nil {
		return nil, fmt.Errorf("commandrisk: no advisor configured")
	}
	user := fmt.Sprintf("Command:\n%s\n\nDetected commands: %v", command, detected.Commands)
	out, err := a.complete(ctx, advisorSystemPrompt, user, a.model, 400)
	if err != nil {
		return nil, fmt.Errorf("commandrisk: advisor: %w", err)
	}
	advice, err := parseAdvice(out)
	if err != nil {
		return nil, err
	}
	return advice, nil
}

// parseAdvice extracts the JSON advice object from a model response, tolerating
// surrounding prose or code fences.
func parseAdvice(out string) (*LLMAdvice, error) {
	start := strings.IndexByte(out, '{')
	end := strings.LastIndexByte(out, '}')
	if start < 0 || end <= start {
		return nil, fmt.Errorf("commandrisk: advisor returned no JSON object")
	}
	var advice LLMAdvice
	if err := json.Unmarshal([]byte(out[start:end+1]), &advice); err != nil {
		return nil, fmt.Errorf("commandrisk: advisor JSON: %w", err)
	}
	return &advice, nil
}
