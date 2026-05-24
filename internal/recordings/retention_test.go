package recordings

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPurgeExpired_deletesOldHrec(t *testing.T) {
	dir := t.TempDir()
	name := "old_web-cue-exec_batch_mixed_batch-1.hrec.jsonl"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(`{"time_ms":0,"type":"open"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	keep := "new_web-cue-exec_batch_mixed_batch-1.hrec.jsonl"
	keepPath := filepath.Join(dir, keep)
	if err := os.WriteFile(keepPath, []byte(`{"time_ms":0,"type":"open"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := PurgeExpired(dir, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 1 {
		t.Fatalf("deleted=%d want 1", res.Deleted)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("old file still exists")
	}
	if _, err := os.Stat(keepPath); err != nil {
		t.Fatalf("new file removed: %v", err)
	}
}
