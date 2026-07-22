package workspacestore

import (
	"context"
	"encoding/json"
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
