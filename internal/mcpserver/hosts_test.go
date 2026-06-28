package mcpserver

import (
	"context"
	"slices"
	"testing"

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
