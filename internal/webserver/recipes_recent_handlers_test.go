package webserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeFakeRecording writes a minimal hrec.jsonl with open + recipe-meta events.
func writeFakeRecording(t *testing.T, dir, name, recipePath string, hostCount int, hash string, started time.Time) {
	t.Helper()
	open := fmt.Sprintf(`{"time_ms":0,"type":"open","message":"trigger=web-cue-exec mode=batch provider=mixed host=batch-%d ip= user=ops"}`+"\n", hostCount)
	meta := fmt.Sprintf(`{"time_ms":1,"type":"recipe-meta","result":{"recipe_path":"%s","host_count":%d,"recipe_content_hash":"%s","started_at":"%s"}}`+"\n",
		recipePath, hostCount, hash, started.Format(time.RFC3339Nano))
	if err := os.WriteFile(filepath.Join(dir, name), []byte(open+meta), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRecentRuns_ordersByMostRecent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFakeRecording(t, dir,
		"20260512_080000_web-cue-exec_batch_mixed_batch-3.hrec.jsonl",
		"examples/recipe/a.cue", 3, "sha256:aaaaaa",
		time.Date(2026, 5, 12, 8, 0, 0, 0, time.UTC))
	writeFakeRecording(t, dir,
		"20260512_090000_web-cue-exec_batch_mixed_batch-5.hrec.jsonl",
		"examples/recipe/b.cue", 5, "sha256:bbbbbb",
		time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC))

	s, err := NewServer(Options{ListenAddr: "127.0.0.1:0", Token: "tok", Version: "0", RecordDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/recipes/recent-runs?limit=10", nil)
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	s.withAuth(s.handleRecipesRecentRuns)(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	var got struct {
		Runs []struct {
			RecipeName string `json:"recipe_name"`
			HostCount  int    `json:"host_count"`
			Edited     bool   `json:"edited"`
		} `json:"runs"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if len(got.Runs) != 2 {
		t.Fatalf("expected 2 runs, got %d (body: %s)", len(got.Runs), w.Body)
	}
	if got.Runs[0].RecipeName != "b.cue" {
		t.Fatalf("expected most-recent (b.cue) first, got %s", got.Runs[0].RecipeName)
	}
}

func TestRecentRuns_limitHonored(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		writeFakeRecording(t, dir,
			fmt.Sprintf("20260512_%02d0000_web-cue-exec_batch_mixed_batch-1.hrec.jsonl", i),
			"examples/recipe/x.cue", 1, "sha256:x",
			time.Date(2026, 5, 12, i, 0, 0, 0, time.UTC))
	}
	s, _ := NewServer(Options{ListenAddr: "127.0.0.1:0", Token: "tok", Version: "0", RecordDir: dir})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/recipes/recent-runs?limit=3", nil)
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	s.withAuth(s.handleRecipesRecentRuns)(w, req)
	var got struct {
		Runs []json.RawMessage `json:"runs"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if len(got.Runs) != 3 {
		t.Fatalf("limit=3 returned %d", len(got.Runs))
	}
}

func TestRecentRuns_skipsDryRuns(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFakeRecording(t, dir,
		"20260512_080000_web-cue-exec-dry_batch_mixed_batch-3.hrec.jsonl",
		"examples/recipe/dry.cue", 3, "sha256:dry",
		time.Date(2026, 5, 12, 8, 0, 0, 0, time.UTC))
	writeFakeRecording(t, dir,
		"20260512_090000_web-cue-exec_batch_mixed_batch-5.hrec.jsonl",
		"examples/recipe/real.cue", 5, "sha256:real",
		time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC))

	s, _ := NewServer(Options{ListenAddr: "127.0.0.1:0", Token: "tok", Version: "0", RecordDir: dir})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/recipes/recent-runs", nil)
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	s.withAuth(s.handleRecipesRecentRuns)(w, req)
	var got struct {
		Runs []struct {
			RecipeName string `json:"recipe_name"`
		} `json:"runs"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if len(got.Runs) != 1 || got.Runs[0].RecipeName != "real.cue" {
		t.Fatalf("expected only real.cue, got: %v", got.Runs)
	}
}

func TestRecentRuns_emptyDirOK(t *testing.T) {
	t.Parallel()
	s, _ := NewServer(Options{ListenAddr: "127.0.0.1:0", Token: "tok", Version: "0", RecordDir: ""})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/recipes/recent-runs", nil)
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	s.withAuth(s.handleRecipesRecentRuns)(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
}
