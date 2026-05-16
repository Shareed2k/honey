package stackunwrap

import (
	"context"
	"strings"
	"testing"
)

type stubUnwrapper struct {
	name     string
	prefix   string
	keyBytes []byte
}

func (s stubUnwrapper) Name() string { return s.name }

func (s stubUnwrapper) Supports(providerURL string) bool {
	return strings.HasPrefix(providerURL, s.prefix)
}

func (s stubUnwrapper) Unwrap(_ context.Context, _, _ string) ([]byte, error) {
	return s.keyBytes, nil
}

func TestRegistry_Unwrap_firstMatch(t *testing.T) {
	r := NewRegistry()
	key := make([]byte, 32)
	r.Register(stubUnwrapper{name: "a", prefix: "test://", keyBytes: key})
	got, err := r.Unwrap(context.Background(), "test://x", "ignored")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(key) {
		t.Fatalf("got %d bytes", len(got))
	}
}

func TestRegistry_Unwrap_unsupported(t *testing.T) {
	r := NewRegistry()
	_, err := r.Unwrap(context.Background(), "unknown://", "x")
	if err == nil {
		t.Fatal("expected error")
	}
}
