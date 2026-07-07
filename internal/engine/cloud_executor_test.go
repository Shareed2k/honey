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

func TestRecipeRunner_DryRun_AwsGcp(t *testing.T) {
	r := engine.NewRecipeRunner(engine.RunnerOptions{})

	const cloudRecipe = `
recipe: {
	name: "cloud-test"
	type: "graph"
	steps: [
		{ id: "aws_test", host: "_", aws: { service: "s3", operation: "create_bucket", params: { bucket: "test-bucket" } } },
		{ id: "gcp_test", host: "_", depends: ["aws_test"], gcp: { service: "storage", operation: "create_bucket", params: { bucket: "test-bucket-gcp" } } },
	]
}
`
	out, err := r.DryRun(context.Background(), engine.RunRequest{
		Recipe:  parseTestRecipe(t, cloudRecipe),
		Records: []hosts.Record{{Provider: "static", Name: "h1", PrimaryIP: "127.0.0.1"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "kind=aws service=\"s3\" operation=\"create_bucket\" host=\"_\"") {
		t.Errorf("expected aws step to dry run successfully, got: %s", out)
	}
	if !strings.Contains(out, "kind=gcp service=\"storage\" operation=\"create_bucket\" host=\"_\"") {
		t.Errorf("expected gcp step to dry run successfully, got: %s", out)
	}
}
