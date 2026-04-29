package hosts

import (
	"context"
	"testing"
)

type stubBackend struct {
	id   string
	name string
}

func (s stubBackend) ID() string                            { return s.id }
func (s stubBackend) BackendName() string                  { return s.name }
func (s stubBackend) CacheIdentity() string                  { return s.name + "|id" }
func (s stubBackend) Search(_ context.Context, _ Query) ([]Record, error) {
	return nil, nil
}

func TestParseBackendNames(t *testing.T) {
	t.Parallel()
	got := ParseBackendNames(" Foo , BAR ")
	if len(got) != 2 || got[0] != "foo" || got[1] != "bar" {
		t.Fatalf("got %#v", got)
	}
	if ParseBackendNames("") != nil || ParseBackendNames("  ,  ") != nil {
		t.Fatal("expected nil for empty")
	}
}

func TestFilterBackendsByNames(t *testing.T) {
	t.Parallel()
	provs := []Backend{
		stubBackend{id: "gcp", name: "gcp-prod-us2"},
		stubBackend{id: "gcp", name: "gcp-stg2"},
		stubBackend{id: "k8s", name: ""},
	}
	out := FilterBackendsByNames(provs, []string{"gcp-prod-us2"})
	if len(out) != 1 {
		t.Fatalf("len %d", len(out))
	}
	if out[0].(stubBackend).name != "gcp-prod-us2" {
		t.Fatal(out)
	}
	if len(FilterBackendsByNames(provs, []string{"nope"})) != 0 {
		t.Fatal("expected empty")
	}
	if len(FilterBackendsByNames(provs, nil)) != len(provs) {
		t.Fatal("nil want should keep all")
	}
}
