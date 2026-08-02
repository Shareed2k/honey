package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hosts"
)

func parseTestRecipe(t *testing.T, content string) cuetry.Recipe {
	t.Helper()
	rec, err := cuetry.ParseRemoteRecipe([]byte(content), nil)
	if err != nil {
		t.Fatalf("failed to parse test recipe: %v", err)
	}
	return rec
}

func TestRecipeRunner_DryRun_PackageService(t *testing.T) {
	r := engine.NewRecipeRunner(engine.RunnerOptions{})

	const osRecipe = `
recipe: {
	name: "os-test"
	type: "graph"
	steps: [
		{ id: "pkg_test", host: "*", package: { name: "nginx", state: "present" } },
		{ id: "svc_test", host: "*", depends: ["pkg_test"], service: { name: "nginx", state: "started" } },
	]
}
`
	out, err := r.DryRun(context.Background(), engine.RunRequest{
		Recipe:  parseTestRecipe(t, osRecipe),
		Records: []hosts.Record{{Provider: "static", Name: "h1", PrimaryIP: "127.0.0.1"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "kind=package name=\"nginx\" state=\"present\" host=\"h1\"") {
		t.Errorf("expected package step to dry run successfully, got: %s", out)
	}
	if !strings.Contains(out, "kind=service name=\"nginx\" state=\"started\" host=\"h1\"") {
		t.Errorf("expected service step to dry run successfully, got: %s", out)
	}
}
