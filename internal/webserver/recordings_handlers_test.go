package webserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHandleRecordingsDelete(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	name := "20260512_090000_web-cue-exec_batch_mixed_batch-1.hrec.jsonl"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"time_ms":0,"type":"open"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(Options{ListenAddr: "127.0.0.1:0", Token: "tok", Version: "0", RecordDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/recordings/"+name, nil)
	req.Header.Set("Authorization", "Bearer tok")
	req.SetPathValue("file_name", name)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
		t.Fatal("file not deleted")
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "deleted" {
		t.Fatalf("body=%v", body)
	}
}
