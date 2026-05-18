package searchrun

import (
	"context"
	"testing"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
)

type mockBackend struct {
	records []hosts.Record
	err     error
}

func (m *mockBackend) ID() string            { return "mock" }
func (m *mockBackend) BackendName() string   { return "mock" }
func (m *mockBackend) CacheIdentity() string { return "mock" }
func (m *mockBackend) Search(_ context.Context, _ hosts.Query) ([]hosts.Record, error) {
	return m.records, m.err
}

func TestDockerDiscoverWrapper(t *testing.T) {
	called := 0
	RegisterDockerDiscover(func(_ context.Context, _ hosts.Query, vms []hosts.Record) ([]hosts.Record, error) {
		called++
		if len(vms) != 1 || vms[0].Name != "vm-a" {
			t.Fatalf("vms = %#v", vms)
		}
		return []hosts.Record{{Provider: "docker", Name: "c1"}}, nil
	})
	t.Cleanup(func() { dockerDiscover = nil })

	base := []hosts.Record{
		{Provider: "gcp", Name: "vm-a", PrimaryIP: "10.0.0.1"},
	}

	w := WithDockerDiscover(&mockBackend{records: base}, config.DockerDiscover{Enabled: true})

	out, err := w.Search(context.Background(), hosts.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatalf("discover called %d times", called)
	}
	if len(out) != 2 {
		t.Fatalf("records = %d, want 2 (1 VM + 1 container)", len(out))
	}
	if out[0].Meta["docker_discover_enabled"] != "1" {
		t.Fatal("expected meta injection on VM")
	}
}

func TestDockerDiscoverWrapperDisabled(t *testing.T) {
	RegisterDockerDiscover(func(_ context.Context, _ hosts.Query, _ []hosts.Record) ([]hosts.Record, error) {
		t.Fatal("should not run")
		return nil, nil
	})
	t.Cleanup(func() { dockerDiscover = nil })

	base := []hosts.Record{{Provider: "gcp", Name: "vm-a", PrimaryIP: "10.0.0.1"}}

	w := WithDockerDiscover(&mockBackend{records: base}, config.DockerDiscover{Enabled: false})

	out, err := w.Search(context.Background(), hosts.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatal("expected unchanged records")
	}
	if out[0].Meta != nil && out[0].Meta["docker_discover_enabled"] == "1" {
		t.Fatal("meta injected when disabled")
	}
}
