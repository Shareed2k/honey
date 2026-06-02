package snippets

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *LocalStore {
	t.Helper()
	return NewLocalStore(filepath.Join(t.TempDir(), "snippets.json"))
}

func TestLocalStoreListMissingFile(t *testing.T) {
	t.Parallel()
	got, err := newTestStore(t).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %+v", got)
	}
}

func TestLocalStoreSaveGensIDAndRoundTrips(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	saved, err := s.Save(ctx, ExecSnippet{Name: "uptime", Mode: "command", Command: "uptime"})
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" {
		t.Fatal("expected generated ID")
	}
	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != saved.ID || list[0].Command != "uptime" {
		t.Fatalf("round-trip mismatch: %+v", list)
	}
}

func TestLocalStoreSaveUpsertsByID(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	saved, _ := s.Save(ctx, ExecSnippet{Name: "a", Mode: "command", Command: "echo a"})
	saved.Command = "echo b"
	if _, err := s.Save(ctx, saved); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List(ctx)
	if len(list) != 1 {
		t.Fatalf("upsert should not duplicate, got %d", len(list))
	}
	if list[0].Command != "echo b" {
		t.Fatalf("update not applied: %+v", list[0])
	}
}

func TestLocalStoreDelete(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	saved, _ := s.Save(ctx, ExecSnippet{Name: "a", Mode: "command", Command: "x"})
	if err := s.Delete(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List(ctx)
	if len(list) != 0 {
		t.Fatalf("want empty after delete, got %+v", list)
	}
	if err := s.Delete(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
