package webserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"
	"go.uber.org/zap"

	agcore "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	agtypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	agsse "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/encoding/sse"
)

// RunAgentInput represents the JSON body of the AG-UI SSE request.
type RunAgentInput struct {
	ThreadID string            `json:"threadId"`
	RunID    string            `json:"runId"`
	Model    string            `json:"model,omitempty"`
	Messages []agtypes.Message `json:"messages"`
	Tools    []agtypes.Tool    `json:"tools,omitempty"`
}

//nolint:gocyclo
func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 1. Validate auth and rate limits
	if assistAPIKey() == "" {
		http.Error(w, "AI assist not configured", http.StatusServiceUnavailable)
		return
	}
	if !s.assistRL.allow(r.RemoteAddr, assistRPM()) {
		http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	// 2. Set headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Connection", "keep-alive")

	var input RunAgentInput
	if err := json.NewDecoder(io.LimitReader(r.Body, maxAssistRequestBody)).Decode(&input); err != nil {
		zap.L().Error("failed to decode agent input", zap.Error(err))
		return // Too late to send HTTP error if we already sent headers? Actually we just set them.
	}

	sseWriter := agsse.NewSSEWriter()
	err := sseWriter.WriteEvent(ctx, w, agcore.NewRunStartedEvent(input.RunID, input.ThreadID))
	if err != nil {
		zap.L().Error("failed to write RunStartedEvent", zap.Error(err))
		return
	}

	// 3. Convert AG-UI messages to OpenAI format
	var openAIMessages []openai.ChatCompletionMessage
	for _, msg := range input.Messages {
		contentStr, _ := msg.ContentString()
		oaiMsg := openai.ChatCompletionMessage{
			Role:    string(msg.Role),
			Content: contentStr,
		}

		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				toolName := strings.TrimPrefix(tc.Function.Name, "default_api:")
				oaiMsg.ToolCalls = append(oaiMsg.ToolCalls, openai.ToolCall{
					ID:   tc.ID,
					Type: openai.ToolType(tc.Type),
					Function: openai.FunctionCall{
						Name:      toolName,
						Arguments: tc.Function.Arguments,
					},
				})
			}
		}

		if msg.Role == "tool" {
			oaiMsg.Role = openai.ChatMessageRoleTool
			oaiMsg.ToolCallID = msg.ToolCallID
		}

		openAIMessages = append(openAIMessages, oaiMsg)
	}

	// 4. Convert AG-UI tools to OpenAI format
	var openaiTools []openai.Tool
	for _, t := range input.Tools {
		openaiTools = append(openaiTools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}

	model := input.Model
	if model == "" {
		model = r.URL.Query().Get("model")
	}
	if model == "" {
		ids, _ := s.getAssistModelIDs(ctx, false)
		if len(ids) > 0 {
			model = ids[0]
		}
		if model == "" {
			model = openai.GPT4oMini
		}
	}

	resolveCtx, resolveCancel := context.WithTimeout(ctx, 25*time.Second)
	chatModel, err := s.resolveAssistChatModel(resolveCtx, model)
	resolveCancel()
	if err != nil {
		zap.L().Error("agent model resolve failed", zap.Error(err))
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	req := openai.ChatCompletionRequest{
		Model:    chatModel,
		Messages: openAIMessages,
		Stream:   true,
	}
	if len(openaiTools) > 0 {
		req.Tools = openaiTools
	}

	client := assistNewOpenAIClient()
	stream, err := client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		zap.L().Error("agent stream creation failed", zap.Error(err))
		_ = sseWriter.WriteEvent(ctx, w, agcore.NewRunErrorEvent(err.Error()))
		return
	}
	defer stream.Close()

	var currentTextMsgID string
	var currentToolCallID string

	hasTextStarted := false

	// Flush to ensure client gets headers and RunStarted immediately
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			zap.L().Error("agent stream recv failed", zap.Error(err))
			_ = sseWriter.WriteEvent(ctx, w, agcore.NewRunErrorEvent(err.Error()))
			return
		}

		if len(resp.Choices) == 0 {
			continue
		}

		delta := resp.Choices[0].Delta

		// Handle Text
		if delta.Content != "" {
			if !hasTextStarted {
				currentTextMsgID = resp.ID // Just use response ID
				_ = sseWriter.WriteEvent(ctx, w, agcore.NewTextMessageStartEvent(currentTextMsgID))
				hasTextStarted = true
			}
			_ = sseWriter.WriteEvent(ctx, w, agcore.NewTextMessageContentEvent(currentTextMsgID, delta.Content))
		}

		// Handle Tool Calls
		if len(delta.ToolCalls) > 0 {
			for _, tc := range delta.ToolCalls {
				// ToolCallStart
				if tc.ID != "" {
					if currentToolCallID != "" {
						_ = sseWriter.WriteEvent(ctx, w, agcore.NewToolCallEndEvent(currentToolCallID))
					}
					currentToolCallID = tc.ID

					toolName := strings.TrimPrefix(tc.Function.Name, "default_api:")
					_ = sseWriter.WriteEvent(ctx, w, agcore.NewToolCallStartEvent(tc.ID, toolName))
				}
				// ToolCallArgs
				if tc.Function.Arguments != "" {
					_ = sseWriter.WriteEvent(ctx, w, agcore.NewToolCallArgsEvent(currentToolCallID, tc.Function.Arguments))
				}
			}
		}

		// Handle Finish
		if resp.Choices[0].FinishReason != "" {
			finishReason := resp.Choices[0].FinishReason

			if hasTextStarted {
				_ = sseWriter.WriteEvent(ctx, w, agcore.NewTextMessageEndEvent(currentTextMsgID))
				hasTextStarted = false
			}

			if currentToolCallID != "" {
				_ = sseWriter.WriteEvent(ctx, w, agcore.NewToolCallEndEvent(currentToolCallID))
				currentToolCallID = ""
			}

			switch finishReason {
			case openai.FinishReasonToolCalls:
				// End run but allow continuation by caller
				_ = sseWriter.WriteEvent(ctx, w, agcore.NewRunFinishedEvent(input.RunID, input.ThreadID))
				return
			case openai.FinishReasonStop:
				_ = sseWriter.WriteEvent(ctx, w, agcore.NewRunFinishedEvent(input.RunID, input.ThreadID))
				return
			}
		}

		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}

	// Just in case it ends without finish reason
	if hasTextStarted {
		_ = sseWriter.WriteEvent(ctx, w, agcore.NewTextMessageEndEvent(currentTextMsgID))
	}
	if currentToolCallID != "" {
		_ = sseWriter.WriteEvent(ctx, w, agcore.NewToolCallEndEvent(currentToolCallID))
	}
	_ = sseWriter.WriteEvent(ctx, w, agcore.NewRunFinishedEvent(input.RunID, input.ThreadID))
}
