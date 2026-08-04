package hosts

import (
	"context"
	"testing"
)

type stubBackend struct {
	id   string
	name string
}

func (s stubBackend) ID() string { return s.id }

func (s stubBackend) BackendName() string { return s.name }

func (s stubBackend) CacheIdentity() string { return s.name + "|id" }
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

func TestFilterBackendsByNamesKindName(t *testing.T) {
	t.Parallel()
	provs := []Backend{
		stubBackend{id: "truenas", name: "prod"},
		stubBackend{id: "proxmox", name: "prod"},
		stubBackend{id: "k8s", name: "prod"},
	}
	out := FilterBackendsByNames(provs, []string{"truenas:prod"})
	if len(out) != 1 {
		t.Fatalf("len %d, want 1", len(out))
	}
	if out[0].(stubBackend).id != "truenas" {
		t.Fatalf("got %#v", out[0])
	}
	out = FilterBackendsByNames(provs, []string{"proxmox:prod"})
	if len(out) != 1 || out[0].(stubBackend).id != "proxmox" {
		t.Fatalf("proxmox: %#v", out)
	}
	out = FilterBackendsByNames(provs, []string{"kubernetes:prod"})
	if len(out) != 1 || out[0].(stubBackend).id != "k8s" {
		t.Fatalf("kubernetes: %#v", out)
	}
	out = FilterBackendsByNames(provs, []string{"k8s:prod"})
	if len(out) != 1 || out[0].(stubBackend).id != "k8s" {
		t.Fatalf("k8s: %#v", out)
	}
}

func TestFilterBackendsByNamesLegacyNameOnly(t *testing.T) {
	t.Parallel()
	provs := []Backend{
		stubBackend{id: "truenas", name: "prod"},
		stubBackend{id: "proxmox", name: "prod"},
	}
	out := FilterBackendsByNames(provs, []string{"prod"})
	if len(out) != 2 {
		t.Fatalf("legacy name-only should match both: len %d", len(out))
	}
}

func TestFilterBackendsByNamesHoney(t *testing.T) {
	t.Parallel()
	provs := []Backend{
		stubBackend{id: "honey", name: "gcp-pme"},
		stubBackend{id: "honey", name: "mesh-peer"},
		stubBackend{id: "k8s", name: "prod"},
	}

	// Naming one honey proxy runs ONLY it — other honey proxies must not tag along.
	out := FilterBackendsByNames(provs, []string{"gcp-pme"})
	if len(out) != 1 || out[0].(stubBackend).name != "gcp-pme" {
		t.Fatalf("naming gcp-pme should select only it, got %#v", out)
	}

	// A token naming nothing local is presumed upstream: every honey proxy runs
	// to forward it; local non-honey backends do not.
	out = FilterBackendsByNames(provs, []string{"gcp-prod-us2"})
	if len(out) != 2 {
		t.Fatalf("upstream-only token should hit both honey proxies, got %#v", out)
	}
	for _, p := range out {
		if p.ID() != "honey" {
			t.Fatalf("only honey proxies forward an upstream-only token, got %#v", p)
		}
	}

	// A token naming a local non-honey backend is handled locally; honey proxies
	// are not dragged in.
	out = FilterBackendsByNames(provs, []string{"prod"})
	if len(out) != 1 || out[0].(stubBackend).id != "k8s" {
		t.Fatalf("naming a local k8s backend should not pull in honey, got %#v", out)
	}

	// Explicit honey:name selects that proxy only.
	out = FilterBackendsByNames(provs, []string{"honey:mesh-peer"})
	if len(out) != 1 || out[0].(stubBackend).name != "mesh-peer" {
		t.Fatalf("honey:mesh-peer should select only mesh-peer, got %#v", out)
	}
}
