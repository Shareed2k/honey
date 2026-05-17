package plugins

import "testing"

func TestExtismTimeoutMS(t *testing.T) {
	t.Parallel()
	got, err := extismTimeoutMS(30000)
	if err != nil || got != 30000 {
		t.Fatalf("got %d err=%v", got, err)
	}
	if _, err := extismTimeoutMS(0); err == nil {
		t.Fatal("expected error for zero")
	}
	if _, err := extismTimeoutMS(-1); err == nil {
		t.Fatal("expected error for negative")
	}
	clamped, err := extismTimeoutMS(maxPluginTimeoutMS + 1)
	if err != nil || clamped != maxPluginTimeoutMS {
		t.Fatalf("clamp: got %d err=%v", clamped, err)
	}
}

func TestExtismMemoryPages(t *testing.T) {
	t.Parallel()
	pages, err := extismMemoryPages(32)
	if err != nil || pages < 1 {
		t.Fatalf("32mb: pages=%d err=%v", pages, err)
	}
	if _, err := extismMemoryPages(0); err == nil {
		t.Fatal("expected error for zero")
	}
}
