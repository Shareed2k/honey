package webserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/shareed2k/honey/internal/config"
)

func TestFeedbackHandlers_ConfigErrors(t *testing.T) {
	// 1. Config is nil
	s1, err := NewServer(Options{
		ListenAddr: "127.0.0.1:0",
		Token:      "secret",
		Config:     nil,
	})
	if err != nil {
		t.Fatal(err)
	}

	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/api/v1/logs/feedback", nil)
	s1.handleLogsFeedbackGet(rec1, req1)
	if rec1.Code != http.StatusNotFound {
		t.Errorf("expected 404 when Config is nil, got %d", rec1.Code)
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/logs/feedback", bytes.NewReader([]byte(`{}`)))
	s1.handleLogsFeedbackSave(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Errorf("expected 404 when Config is nil on POST, got %d", rec2.Code)
	}

	// 2. AnomalyFeedbackFile is empty
	s2, err := NewServer(Options{
		ListenAddr: "127.0.0.1:0",
		Token:      "secret",
		Config:     &config.File{Defaults: config.Defaults{}},
	})
	if err != nil {
		t.Fatal(err)
	}

	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/logs/feedback", nil)
	s2.handleLogsFeedbackGet(rec3, req3)
	if rec3.Code != http.StatusNotFound {
		t.Errorf("expected 404 when feedback file is empty, got %d", rec3.Code)
	}
}

func TestFeedbackHandlers_GetNonExistentFile(t *testing.T) {
	dir := t.TempDir()
	feedbackPath := filepath.Join(dir, "non-existent-feedback.jsonl")

	s, err := NewServer(Options{
		ListenAddr: "127.0.0.1:0",
		Token:      "secret",
		Config: &config.File{
			Defaults: config.Defaults{
				Logs: config.Logs{
					AnomalyFeedbackFile: feedbackPath,
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/logs/feedback", nil)
	s.handleLogsFeedbackGet(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK when file doesn't exist, got %d", rec.Code)
	}

	var resp map[string][]feedbackWebRecord
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}

	records, ok := resp["records"]
	if !ok {
		t.Fatal("expected 'records' key in response")
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records, got %d", len(records))
	}
}

func TestFeedbackHandlers_SaveAndGet(t *testing.T) {
	dir := t.TempDir()
	feedbackPath := filepath.Join(dir, "feedback.jsonl")

	s, err := NewServer(Options{
		ListenAddr: "127.0.0.1:0",
		Token:      "secret",
		Config: &config.File{
			Defaults: config.Defaults{
				Logs: config.Logs{
					AnomalyFeedbackFile: feedbackPath,
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Initial Get (should be empty)
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/api/v1/logs/feedback", nil)
	s.handleLogsFeedbackGet(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("GET 1 failed: %d", rec1.Code)
	}

	// 2. Save records
	saveReq := feedbackSaveRequest{
		Records: []feedbackWebRecord{
			{
				Ts:      "2026-06-03T12:00:00Z",
				Source:  "test-source-1",
				Line:    "test log line 1",
				Score:   0.95,
				Reason:  "high score",
				Anomaly: true,
			},
			{
				Ts:      "2026-06-03T12:01:00Z",
				Source:  "test-source-2",
				Line:    "test log line 2",
				Score:   0.21,
				Reason:  "low score",
				Anomaly: false,
			},
		},
	}

	bodyBytes, err := json.Marshal(saveReq)
	if err != nil {
		t.Fatal(err)
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/logs/feedback", bytes.NewReader(bodyBytes))
	s.handleLogsFeedbackSave(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("POST failed: %d, response: %s", rec2.Code, rec2.Body.String())
	}

	// Check file permissions
	info, err := os.Stat(feedbackPath)
	if err != nil {
		t.Fatal(err)
	}
	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("expected file permissions to be 0600, got %o", perm)
	}

	// 3. Get records back
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/logs/feedback", nil)
	s.handleLogsFeedbackGet(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("GET 2 failed: %d", rec3.Code)
	}

	var resp map[string][]feedbackWebRecord
	if err := json.Unmarshal(rec3.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}

	records := resp["records"]
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	if records[0].Source != "test-source-1" || records[0].Line != "test log line 1" || records[0].Score != 0.95 || !records[0].Anomaly {
		t.Errorf("record 0 mismatch: %+v", records[0])
	}
	if records[1].Source != "test-source-2" || records[1].Line != "test log line 2" || records[1].Score != 0.21 || records[1].Anomaly {
		t.Errorf("record 1 mismatch: %+v", records[1])
	}

	// 4. Test bad request body on Save
	rec4 := httptest.NewRecorder()
	req4 := httptest.NewRequest(http.MethodPost, "/api/v1/logs/feedback", bytes.NewReader([]byte(`{malformed json`)))
	s.handleLogsFeedbackSave(rec4, req4)
	if rec4.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request on malformed body, got %d", rec4.Code)
	}
}
