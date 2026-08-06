package engine

import (
	"encoding/json"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestToOpenAITools(t *testing.T) {
	tools := toOpenAITools([]chatTool{
		{Name: "run_a", Description: "step a"}, // no params → empty object schema
		{Name: "finish", Description: "settle", Parameters: json.RawMessage(`{"x":1}`)},
	})
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}
	if tools[0].Type != openai.ToolTypeFunction || tools[0].Function.Name != "run_a" || tools[0].Function.Description != "step a" {
		t.Errorf("tool 0 wrong: %+v", tools[0].Function)
	}
	// no-arg tool gets a valid object schema, not nil.
	if m, ok := tools[0].Function.Parameters.(map[string]any); !ok || m["type"] != "object" {
		t.Errorf("tool 0 params = %#v, want an object schema", tools[0].Function.Parameters)
	}
	if rw, ok := tools[1].Function.Parameters.(json.RawMessage); !ok || string(rw) != `{"x":1}` {
		t.Errorf("tool 1 params = %#v, want the raw schema", tools[1].Function.Parameters)
	}
}

func TestToOpenAIMessages(t *testing.T) {
	msgs := toOpenAIMessages([]chatMessage{
		{Role: chatRoleSystem, Content: "sys"},
		{Role: chatRoleUser, Content: "go"},
		{Role: chatRoleAssistant, Content: "", ToolCalls: []chatToolCall{{ID: "c1", Name: "run_a", Args: "{}"}}},
		{Role: chatRoleTool, ToolCallID: "c1", Content: `{"success":true}`},
	})
	if len(msgs) != 4 {
		t.Fatalf("got %d messages, want 4", len(msgs))
	}
	if msgs[2].Role != chatRoleAssistant || len(msgs[2].ToolCalls) != 1 ||
		msgs[2].ToolCalls[0].ID != "c1" || msgs[2].ToolCalls[0].Function.Name != "run_a" ||
		msgs[2].ToolCalls[0].Type != openai.ToolTypeFunction {
		t.Errorf("assistant tool_call not converted: %+v", msgs[2])
	}
	if msgs[3].Role != openai.ChatMessageRoleTool || msgs[3].ToolCallID != "c1" {
		t.Errorf("tool message not converted: role=%q toolCallID=%q", msgs[3].Role, msgs[3].ToolCallID)
	}
}

func TestNewOpenAIAgent_RequiresKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	if _, err := newOpenAIAgent("gpt-4o"); err == nil {
		t.Fatal("expected an error when OPENAI_API_KEY is unset")
	}
	t.Setenv("OPENAI_API_KEY", "sk-test")
	if _, err := newOpenAIAgent("gpt-4o"); err != nil {
		t.Fatalf("with key set: %v", err)
	}
}
