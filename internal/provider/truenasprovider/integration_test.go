//go:build integration && truenas

package truenasprovider

import (
	"context"
	"os"
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestIntegrationSearch(t *testing.T) {
	url := os.Getenv("TRUENAS_URL")
	key := os.Getenv("TRUENAS_API_KEY")
	if url == "" || key == "" {
		t.Skip("set TRUENAS_URL and TRUENAS_API_KEY")
	}
	b := &TrueNAS{
		Name:             "integration",
		URL:              url,
		APIKey:           key,
		Username:         os.Getenv("TRUENAS_USER"),
		Insecure:         os.Getenv("TRUENAS_INSECURE") == "1",
		IncludeAppliance: true,
		IncludeVMs:       true,
		IncludeVirt:      true,
	}
	recs, err := b.Search(context.Background(), hosts.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) == 0 {
		t.Fatal("expected at least appliance record")
	}
}
