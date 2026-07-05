package mcpserver

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostapi"
	"github.com/shareed2k/honey/internal/hosts"
)

// withFakeSearch swaps findHostFn for the duration of the test.
func withFakeSearch(t *testing.T, fn func(context.Context, *hostapi.SearchHostsInput) (hostapi.SearchHostsOutput, error)) {
	t.Helper()
	prev := findHostFn
	findHostFn = fn
	t.Cleanup(func() { findHostFn = prev })
}

func TestHandleGetHostDetails_found(t *testing.T) {
	rec := hosts.Record{
		Name:      "web-prod-1",
		Provider:  "gcp",
		PrimaryIP: "10.0.0.1",
		Groups:    []string{"web", "prod"},
		Meta:      map[string]string{"env": "prod"},
	}
	withFakeSearch(t, func(_ context.Context, _ *hostapi.SearchHostsInput) (hostapi.SearchHostsOutput, error) {
		return hostapi.SearchHostsOutput{Records: []hosts.Record{rec}, Count: 1}, nil
	})

	in := getHostDetailsInput{Name: "web-prod-1"}
	_, out, err := handleGetHostDetails(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Record.Name != "web-prod-1" {
		t.Errorf("Name = %q, want web-prod-1", out.Record.Name)
	}
	if out.Record.Provider != "gcp" {
		t.Errorf("Provider = %q, want gcp", out.Record.Provider)
	}
	if out.Record.PrimaryIP != "10.0.0.1" {
		t.Errorf("PrimaryIP = %q, want 10.0.0.1", out.Record.PrimaryIP)
	}
	if len(out.Capabilities) == 0 {
		t.Errorf("expected non-empty Capabilities for connectable host")
	}
}

func TestHandleGetHostDetails_notFound(t *testing.T) {
	withFakeSearch(t, func(_ context.Context, _ *hostapi.SearchHostsInput) (hostapi.SearchHostsOutput, error) {
		return hostapi.SearchHostsOutput{Records: []hosts.Record{}, Count: 0}, nil
	})

	in := getHostDetailsInput{Name: "no-such-host"}
	_, _, err := handleGetHostDetails(context.Background(), nil, in)
	if err == nil {
		t.Fatal("expected error when host not found")
	}
}

func TestHandleGetHostDetails_emptyName_error(t *testing.T) {
	in := getHostDetailsInput{Name: ""}
	_, _, err := handleGetHostDetails(context.Background(), nil, in)
	if err == nil {
		t.Fatal("expected validation error for empty name")
	}
}

func TestHandleSearchHosts_success(t *testing.T) {
	rec := hosts.Record{
		Name:      "redis-1",
		Provider:  "gcp",
		PrimaryIP: "10.0.0.2",
	}
	withFakeSearch(t, func(_ context.Context, in *hostapi.SearchHostsInput) (hostapi.SearchHostsOutput, error) {
		if in.NameRegex != "redis" {
			t.Errorf("expected NameRegex=redis, got %q", in.NameRegex)
		}
		if in.Providers != "gcp" {
			t.Errorf("expected Providers=gcp, got %q", in.Providers)
		}
		return hostapi.SearchHostsOutput{Records: []hosts.Record{rec}, Count: 1}, nil
	})

	in := searchHostsInput{NameRegex: "redis", Providers: "gcp"}
	_, out, err := handleSearchHosts(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Count != 1 {
		t.Fatalf("Count = %d, want 1", out.Count)
	}
	if out.Records[0].Name != "redis-1" {
		t.Errorf("Records[0].Name = %q, want redis-1", out.Records[0].Name)
	}
}

func TestHandleSearchHosts_error(t *testing.T) {
	withFakeSearch(t, func(_ context.Context, _ *hostapi.SearchHostsInput) (hostapi.SearchHostsOutput, error) {
		return hostapi.SearchHostsOutput{}, fmt.Errorf("search failed")
	})

	in := searchHostsInput{NameRegex: "redis"}
	_, _, err := handleSearchHosts(context.Background(), nil, in)
	if err == nil {
		t.Fatal("expected error from search")
	}
	if err.Error() != "search failed" {
		t.Errorf("expected 'search failed', got %v", err)
	}
}

func withFakeListBackends(t *testing.T, fn func(configPath string) (hostapi.ListBackendsOutput, error)) {
	t.Helper()
	prev := listBackendsFn
	listBackendsFn = fn
	t.Cleanup(func() { listBackendsFn = prev })
}

func TestHandleListBackends_success(t *testing.T) {
	withFakeListBackends(t, func(configPath string) (hostapi.ListBackendsOutput, error) {
		return hostapi.ListBackendsOutput{
			ConfigPath: configPath,
			Backends: []config.BackendRow{
				{Kind: "gcp", Name: "gcp-prod", Hint: "gcp project"},
			},
		}, nil
	})

	in := listBackendsInput{ConfigPath: "testdata/dummy.yaml"}
	_, out, err := handleListBackends(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Backends) != 1 {
		t.Fatalf("len(Backends) = %d, want 1", len(out.Backends))
	}
	if out.Backends[0].Name != "gcp-prod" {
		t.Errorf("Backends[0].Name = %q, want gcp-prod", out.Backends[0].Name)
	}
}

func TestHandleListBackends_error(t *testing.T) {
	withFakeListBackends(t, func(_ string) (hostapi.ListBackendsOutput, error) {
		return hostapi.ListBackendsOutput{}, fmt.Errorf("list backends failed")
	})

	in := listBackendsInput{}
	_, _, err := handleListBackends(context.Background(), nil, in)
	if err == nil {
		t.Fatal("expected error from list_backends")
	}
	if err.Error() != "list backends failed" {
		t.Errorf("expected 'list backends failed', got %v", err)
	}
}

func TestHandleGetHostDetails_dockerCapability(t *testing.T) {
	rec := hosts.Record{
		Name:      "container-1",
		Provider:  "docker",
		PrimaryIP: "172.17.0.2",
		Meta:      map[string]string{"kind": "container", "container_id": "abc123"},
	}
	withFakeSearch(t, func(_ context.Context, _ *hostapi.SearchHostsInput) (hostapi.SearchHostsOutput, error) {
		return hostapi.SearchHostsOutput{Records: []hosts.Record{rec}, Count: 1}, nil
	})

	in := getHostDetailsInput{Name: "container-1"}
	_, out, err := handleGetHostDetails(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Contains(out.Capabilities, "docker_exec") {
		t.Errorf("expected docker_exec capability, got %v", out.Capabilities)
	}
}
