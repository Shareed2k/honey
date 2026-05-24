package main

import "testing"

func TestRCActionPath_core(t *testing.T) {
	t.Parallel()
	for action, want := range map[string]string{
		"noop": "core/noop",
		"copy": "sync/copy",
		"sync": "sync/sync",
		"list": "operations/list",
	} {
		got, err := rcActionPath(action)
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if got != want {
			t.Fatalf("%s: got %q want %q", action, got, want)
		}
	}
}

func TestRCActionPath_unknown(t *testing.T) {
	t.Parallel()
	if _, err := rcActionPath("nope"); err == nil {
		t.Fatal("expected error")
	}
}
