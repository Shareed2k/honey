package ui

import (
	"context"
	"testing"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
)

func TestFilterTargetsByWhen_skipsHost(t *testing.T) {
	t.Parallel()
	recipe := cuetry.Recipe{Name: "t"}
	step := cuetry.RecipeStep{
		ID:      "deploy",
		Host:    "*",
		When:    `host.name == "keep"`,
		Command: "echo",
	}
	targets := []hosts.Record{
		{Name: "keep", PrimaryIP: "1.2.3.4"},
		{Name: "skip", PrimaryIP: "5.6.7.8"},
	}
	kept, skipped, err := filterTargetsByWhen(context.Background(), recipe, step, targets, cuetry.NewStepResultStore(), nil, noopKVReader{}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || kept[0].Name != "keep" {
		t.Fatalf("kept=%v", kept)
	}
	if len(skipped) != 1 || !skipped[0].Skipped {
		t.Fatalf("skipped=%v", skipped)
	}
}

func TestAllHostsWhenSkipped(t *testing.T) {
	t.Parallel()
	if !allHostsWhenSkipped([]HostExecResult{{Skipped: true}, {Skipped: true}}) {
		t.Fatal("expected true")
	}
	if allHostsWhenSkipped([]HostExecResult{{Skipped: true}, {Skipped: false}}) {
		t.Fatal("expected false")
	}
}
