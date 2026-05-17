package k8s_test

import (
	"context"
	"testing"

	"github.com/shareed2k/honey/internal/cuetry/secrets/k8s"
)

func TestBackend_Handles(t *testing.T) {
	b := k8s.New()
	if !b.Handles("k8s:default/my-secret/password") {
		t.Fatal()
	}
	if b.Handles("env:X") {
		t.Fatal()
	}
}

func TestBackend_Resolve_malformed(t *testing.T) {
	b := k8s.New()
	_, err := b.Resolve(context.Background(), "k8s:")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestBackend_Resolve_badParts(t *testing.T) {
	b := k8s.New()
	_, err := b.Resolve(context.Background(), "k8s:only/two")
	if err == nil {
		t.Fatal("expected error")
	}
}
