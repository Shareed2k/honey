package webserver

import (
	"testing"

	"github.com/sashabaranov/go-openai"
)

func TestClipRunesTail(t *testing.T) {
	s := string([]rune{'a', 'b', 'c', 'd', 'e'})
	out, clipped := clipRunesTail(s, 3)
	if out != "cde" || !clipped {
		t.Fatalf("got %q clipped=%v", out, clipped)
	}
	out2, clipped2 := clipRunesTail(s, 10)
	if out2 != s || clipped2 {
		t.Fatalf("got %q clipped=%v", out2, clipped2)
	}
}

func TestClipScrollbackByLines(t *testing.T) {
	s := "a\nb\nc\nd"
	out, clipped := clipScrollbackByLines(s, 2)
	if out != "c\nd" || !clipped {
		t.Fatalf("got %q clipped=%v", out, clipped)
	}
}

func TestAssistExtractAssistantReply(t *testing.T) {
	t.Run("plain_content", func(t *testing.T) {
		got, err := assistExtractAssistantReply(openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{
				{Message: openai.ChatCompletionMessage{Content: "  hello  "}},
			},
		})
		if err != nil || got != "hello" {
			t.Fatalf("got %q err=%v", got, err)
		}
	})
	t.Run("reasoning_content_fallback", func(t *testing.T) {
		got, err := assistExtractAssistantReply(openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{
				{Message: openai.ChatCompletionMessage{ReasoningContent: "think"}},
			},
		})
		if err != nil || got != "think" {
			t.Fatalf("got %q err=%v", got, err)
		}
	})
	t.Run("multi_content_text", func(t *testing.T) {
		got, err := assistExtractAssistantReply(openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{
				{Message: openai.ChatCompletionMessage{
					MultiContent: []openai.ChatMessagePart{
						{Type: openai.ChatMessagePartTypeText, Text: "a"},
						{Type: openai.ChatMessagePartTypeText, Text: "b"},
					},
				}},
			},
		})
		if err != nil || got != "a\nb" {
			t.Fatalf("got %q err=%v", got, err)
		}
	})
	t.Run("no_choices", func(t *testing.T) {
		_, err := assistExtractAssistantReply(openai.ChatCompletionResponse{ID: "x", Model: "m"})
		if err == nil {
			t.Fatal("expected error")
		}
	})
}
