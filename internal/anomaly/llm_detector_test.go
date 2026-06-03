package anomaly

import (
	"strings"
	"testing"
)

func TestLLMDetector_BuildSystemPrompt(t *testing.T) {
	detector := newLLMDetector("http://localhost:11434", "llama3", 0.5, 0)

	tests := []struct {
		name             string
		logLine          string
		expectedContains []string
	}{
		{
			name:    "PostgreSQL Anomaly",
			logLine: "2026-06-02 12:00:00 UTC [12345] [error] password authentication failed for user alice",
			expectedContains: []string{
				"Here are contextually relevant examples of logs and their correct classification:",
				"password authentication failed",
				"failed database authentication attempt",
			},
		},
		{
			name:    "Nginx Normal HTTP GET",
			logLine: `127.0.0.1 - - [02/Jun/2026:12:00:00 +0000] "GET /index.html HTTP/1.1" 200 1234`,
			expectedContains: []string{
				"Here are contextually relevant examples of logs and their correct classification:",
				"GET <*> HTTP/1.1",
				"routine HTTP GET request with 200 success response",
			},
		},
		{
			name:    "Node.js Server Listening",
			logLine: "info: server listening on port 3000",
			expectedContains: []string{
				"Here are contextually relevant examples of logs and their correct classification:",
				"server listening on port",
				"routine application server initialization log",
			},
		},
		{
			name:    "Fallback Unrelated Log",
			logLine: "completely random and unrelated message with no overlap",
			expectedContains: []string{
				"You are an expert system log analyst.",
				"info server started on port <num>",
				"error authentication failed for user root",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalized := normalize(tt.logLine)
			prompt := detector.buildSystemPrompt(normalized)

			for _, sub := range tt.expectedContains {
				if !strings.Contains(prompt, sub) {
					t.Errorf("expected prompt to contain %q, but it did not.\nPrompt:\n%s", sub, prompt)
				}
			}
		})
	}
}
