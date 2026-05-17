package env_test

import (
	"context"
	"testing"

	"github.com/shareed2k/honey/internal/cuetry/secrets/env"
)

func TestBackend_Handles(t *testing.T) {
	b := env.New()
	if !b.Handles("env:FOO") {
		t.Fatal("expected Handles true")
	}
	if b.Handles("aws-sm:x") {
		t.Fatal("expected Handles false")
	}
}

func TestBackend_Resolve(t *testing.T) {
	const key = "HONEY_TEST_ENV_SECRET"
	t.Setenv(key, "secret-value")
	b := env.New()
	got, err := b.Resolve(context.Background(), "env:"+key)
	if err != nil {
		t.Fatal(err)
	}
	if got != "secret-value" {
		t.Fatalf("got %q", got)
	}
}

func TestBackend_Resolve_missing(t *testing.T) {
	b := env.New()
	_, err := b.Resolve(context.Background(), "env:HONEY_TEST_ENV_MISSING_XYZ")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestBackend_Resolve_emptyName(t *testing.T) {
	b := env.New()
	_, err := b.Resolve(context.Background(), "env:")
	if err == nil {
		t.Fatal("expected error")
	}
}
