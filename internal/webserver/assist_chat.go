package webserver

import (
	"context"
	"strings"

	"github.com/sashabaranov/go-openai"
	"go.uber.org/zap"
)

// assistCreateChatCompletion runs a non-streaming chat completion with shared timeouts and token limits.
func assistCreateChatCompletion(ctx context.Context, chatModel, systemPrompt, userContent string) (string, error) {
	client := assistNewOpenAIClient()
	ctx2, cancel := context.WithTimeout(ctx, assistUpstreamTimeout())
	defer cancel()

	zap.L().Debug(
		"assist CreateChatCompletion",
		zap.String("model", chatModel),
		zap.Int("max_tokens", assistMaxTokens()),
		zap.Duration("timeout", assistUpstreamTimeout()),
	)

	resp, err := client.CreateChatCompletion(ctx2, openai.ChatCompletionRequest{
		Model: chatModel,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: userContent},
		},
		MaxTokens:   assistMaxTokens(),
		Temperature: 0.2,
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
