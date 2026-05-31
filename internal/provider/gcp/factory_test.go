package gcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/searchrun"
)

// unwrapGCP extracts the GCP project and zone from a backend returned by
// gcpFactory.Default. The backend is a *searchrun.dockerDiscoverWrapper that
// embeds the real *GCP, so CacheIdentity() is promoted and returns
// "<name>\x1e<project>\x1e<zone>".
func unwrapGCP(b interface {
	CacheIdentity() string
},
) (project, zone string) {
	parts := strings.SplitN(b.CacheIdentity(), "\x1e", 3)
	if len(parts) == 3 {
		return parts[1], parts[2]
	}
	return "", ""
}

func TestDefault_FallsBackToCliFlags(t *testing.T) {
	cliFlags.project = "from-cli"
	cliFlags.zone = "us-central1-a"
	t.Cleanup(func() {
		cliFlags.project = ""
		cliFlags.zone = ""
	})

	b := gcpFactory{}.Default(searchrun.ProviderOverrides{})

	if b.ID() != "gcp" {
		t.Fatalf("ID: want %q got %q", "gcp", b.ID())
	}

	project, zone := unwrapGCP(b)
	if project != "from-cli" {
		t.Errorf("project: want %q got %q", "from-cli", project)
	}
	if zone != "us-central1-a" {
		t.Errorf("zone: want %q got %q", "us-central1-a", zone)
	}
}

func TestDefault_UsesProviderOverrides(t *testing.T) {
	// cliFlags should be ignored when ProviderOverrides has explicit values.
	cliFlags.project = "should-be-ignored"
	cliFlags.zone = "should-be-ignored"
	t.Cleanup(func() {
		cliFlags.project = ""
		cliFlags.zone = ""
	})

	raw, _ := json.Marshal(config.GCPBackend{Project: "api-proj", Zone: "eu-west1-b"})
	b := gcpFactory{}.Default(searchrun.ProviderOverrides{"gcp": raw})

	if b.ID() != "gcp" {
		t.Fatalf("ID: want %q got %q", "gcp", b.ID())
	}

	project, zone := unwrapGCP(b)
	if project != "api-proj" {
		t.Errorf("project: want %q got %q", "api-proj", project)
	}
	if zone != "eu-west1-b" {
		t.Errorf("zone: want %q got %q", "eu-west1-b", zone)
	}
}
