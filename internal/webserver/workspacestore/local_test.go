package workspacestore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestLoadMissingReturnsZero(t *testing.T) {
	l := New(t.TempDir())
	ws, err := l.Load(context.Background())
	if err != nil {
		t.Fatalf("Load on empty dir: %v", err)
	}
	if ws.Layout != nil || len(ws.OpenRecipes) != 0 || ws.Active != "" {
		t.Fatalf("expected zero Workspace, got %+v", ws)
	}
}

func TestSaveThenLoadRoundTrip(t *testing.T) {
	l := New(t.TempDir())
	in := Workspace{Layout: json.RawMessage(`{"k":1}`), OpenRecipes: []string{"a.cue", "b.cue"}, Active: "a.cue"}
	if err := l.Save(context.Background(), in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := l.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(out.Layout) != `{"k":1}` || out.Active != "a.cue" || len(out.OpenRecipes) != 2 {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
}

func TestConcurrentSaveNoRace(t *testing.T) {
	l := New(t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(_ int) {
			defer wg.Done()
			_ = l.Save(context.Background(), Workspace{Active: "x", Layout: json.RawMessage(`{}`)})
		}(i)
	}
	wg.Wait()
	if _, err := l.Load(context.Background()); err != nil {
		t.Fatalf("Load after concurrent saves: %v", err)
	}
}

// TestConcurrentSaveAcrossTwoInstancesNoRace proves that two independent Local
// instances pointed at the same directory don't corrupt each other's writes.
// Before the unique-temp-file fix, both instances shared the fixed temp path
// dir/studio_workspace.json.tmp and could race on it; now each Save uses its
// own os.CreateTemp-generated name, so this must pass cleanly under -race.
func TestConcurrentSaveAcrossTwoInstancesNoRace(t *testing.T) {
	dir := t.TempDir()
	l1 := New(dir)
	l2 := New(dir)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(_ int) {
			defer wg.Done()
			_ = l1.Save(context.Background(), Workspace{Active: "x", Layout: json.RawMessage(`{}`)})
		}(i)
		wg.Add(1)
		go func(_ int) {
			defer wg.Done()
			_ = l2.Save(context.Background(), Workspace{Active: "y", Layout: json.RawMessage(`{}`)})
		}(i)
	}
	wg.Wait()

	ws, err := l1.Load(context.Background())
	if err != nil {
		t.Fatalf("Load after concurrent saves across two instances: %v", err)
	}
	if ws.Active != "x" && ws.Active != "y" {
		t.Fatalf("expected a valid, non-corrupted Workspace, got %+v", ws)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover temp file after successful Save: %s", filepath.Join(dir, e.Name()))
		}
	}
}
