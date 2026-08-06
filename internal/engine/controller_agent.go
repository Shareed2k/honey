package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	openai "github.com/sashabaranov/go-openai"
)

// chatAgent is the controller's LLM seam: given a conversation and the available
// tools, return the model's next message (text and/or tool calls). Keeping this
// interface in honey's own tiny types (not go-openai types) is the test surface —
// the controller loop is driven by a scripted fake in tests, and by the go-openai
// adapter (openAIAgent) in production. It is a real seam: two implementations.
type chatAgent interface {
	Chat(ctx context.Context, turn chatTurn) (chatReply, error)
}

type chatTurn struct {
	Messages []chatMessage
	Tools    []chatTool
}

// chatRole values (a subset of the OpenAI roles honey uses).
const (
	chatRoleSystem    = "system"
	chatRoleUser      = "user"
	chatRoleAssistant = "assistant"
	chatRoleTool      = "tool"
)

type chatMessage struct {
	Role       string
	Content    string
	ToolCalls  []chatToolCall // assistant messages: the calls the model requested
	ToolCallID string         // tool messages: which call this is the result for
}

type chatTool struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema for the arguments (an object schema)
}

type chatToolCall struct {
	ID   string
	Name string
	Args string // raw JSON arguments
}

type chatReply struct {
	AssistantText string
	ToolCalls     []chatToolCall
}

// openAIAgent is the production chatAgent, backed by go-openai. Honors
// OPENAI_API_KEY (required) and OPENAI_BASE_URL (optional, for OpenAI-compatible
// endpoints) — the same env the ai/summarize steps use.
type openAIAgent struct {
	client *openai.Client
	model  string
}

func newOpenAIAgent(model string) (*openAIAgent, error) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("controller: OPENAI_API_KEY is required for type: \"controller\"")
	}
	cfg := openai.DefaultConfig(key)
	if base := os.Getenv("OPENAI_BASE_URL"); base != "" {
		cfg.BaseURL = base
	}
	return &openAIAgent{client: openai.NewClientWithConfig(cfg), model: model}, nil
}

func (a *openAIAgent) Chat(ctx context.Context, turn chatTurn) (chatReply, error) {
	req := openai.ChatCompletionRequest{
		Model:    a.model,
		Messages: toOpenAIMessages(turn.Messages),
		Tools:    toOpenAITools(turn.Tools),
	}
	resp, err := a.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return chatReply{}, fmt.Errorf("controller: chat completion: %w", err)
	}
	if len(resp.Choices) == 0 {
		return chatReply{}, fmt.Errorf("controller: model returned no choices")
	}
	msg := resp.Choices[0].Message
	reply := chatReply{AssistantText: msg.Content}
	for _, tc := range msg.ToolCalls {
		reply.ToolCalls = append(reply.ToolCalls, chatToolCall{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: tc.Function.Arguments,
		})
	}
	return reply, nil
}

func toOpenAIMessages(msgs []chatMessage) []openai.ChatCompletionMessage {
	out := make([]openai.ChatCompletionMessage, 0, len(msgs))
	for _, m := range msgs {
		om := openai.ChatCompletionMessage{Role: m.Role, Content: m.Content}
		if m.Role == chatRoleTool {
			om.ToolCallID = m.ToolCallID
		}
		for _, tc := range m.ToolCalls {
			om.ToolCalls = append(om.ToolCalls, openai.ToolCall{
				ID:       tc.ID,
				Type:     openai.ToolTypeFunction,
				Function: openai.FunctionCall{Name: tc.Name, Arguments: tc.Args},
			})
		}
		out = append(out, om)
	}
	return out
}

func toOpenAITools(tools []chatTool) []openai.Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]openai.Tool, 0, len(tools))
	for _, t := range tools {
		var params any = t.Parameters
		if len(t.Parameters) == 0 {
			params = emptyObjectSchema()
		}
		out = append(out, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  params,
			},
		})
	}
	return out
}

// emptyObjectSchema is the JSON Schema for a no-argument tool.
func emptyObjectSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
