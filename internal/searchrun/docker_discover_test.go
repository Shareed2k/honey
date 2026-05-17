package searchrun

import (
	"context"
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestAppendDockerDiscoverRespectsProviderFilter(t *testing.T) {
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
		{Provider: "aws", Name: "vm-b", PrimaryIP: "10.0.0.2"},
	}
	q := hosts.Query{DockerDiscoverProviders: []string{"gcp"}}
	out, err := appendDockerDiscover(context.Background(), q, base)
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatalf("discover called %d times", called)
	}
	if len(out) != 3 {
		t.Fatalf("records = %d, want 3 (2 VMs + 1 container)", len(out))
	}
}

func TestAppendDockerDiscoverNoopWithoutProviders(t *testing.T) {
	RegisterDockerDiscover(func(_ context.Context, _ hosts.Query, _ []hosts.Record) ([]hosts.Record, error) {
		t.Fatal("should not run")
		return nil, nil
	})
	t.Cleanup(func() { dockerDiscover = nil })

	base := []hosts.Record{{Provider: "gcp", Name: "vm-a", PrimaryIP: "10.0.0.1"}}
	out, err := appendDockerDiscover(context.Background(), hosts.Query{}, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatal("expected unchanged records")
	}
}
