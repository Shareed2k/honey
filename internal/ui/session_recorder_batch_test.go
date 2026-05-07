package ui

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewBatchSessionRecorderResultEvents(t *testing.T) {
	dir := t.TempDir()
	rec, err := NewBatchSessionRecorder(dir, "web-exec", "alice", 2)
	if err != nil {
		t.Fatal(err)
	}
	rec.RecordHostExecResult(HostExecResult{Name: "h1", IP: "10.0.0.1", Provider: "ssh", Success: true, ExitCode: 0, Output: "ok"})
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var path string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".hrec.jsonl") {
			path = filepath.Join(dir, e.Name())
			break
		}
	}
	if path == "" {
		t.Fatal("expected a .hrec.jsonl file")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	var types []string
	for sc.Scan() {
		var evt struct {
			Type   string          `json:"type"`
			Result json.RawMessage `json:"result,omitempty"`
		}
		if err := json.Unmarshal(sc.Bytes(), &evt); err != nil {
			t.Fatalf("line json: %v", err)
		}
		types = append(types, evt.Type)
		if evt.Type == "result" && len(evt.Result) == 0 {
			t.Fatal("expected result payload")
		}
	}
	if len(types) < 3 || types[0] != "open" || types[len(types)-1] != "close" {
		t.Fatalf("unexpected event sequence: %v", types)
	}
	found := false
	for _, ty := range types {
		if ty == "result" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected a result event")
	}
}
