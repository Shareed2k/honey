package plugins

import (
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/config"
)

func TestValidateAllowedHosts_rejectsWildcard(t *testing.T) {
	t.Parallel()
	_, err := validateAllowedHosts([]string{"*.example.com"})
	if err == nil || !strings.Contains(err.Error(), "wildcards") {
		t.Fatalf("err=%v", err)
	}
}

func TestValidateAllowedHosts_normalizes(t *testing.T) {
	t.Parallel()
	out, err := validateAllowedHosts([]string{"HTTPS://API.Example.COM:443/path"})
	if err != nil {
		t.Fatal(err)
	}
	if out[0] != "api.example.com:443" {
		t.Fatalf("got %q", out[0])
	}
}

func TestEffectiveNetworkPolicy_globalCap(t *testing.T) {
	t.Parallel()
	m := Manifest{ID: "p", AllowedHosts: []string{"api.example.com"}}
	cfg := config.PluginsEffective{NetworkAllowHosts: []string{"other.example.com"}}
	_, err := effectiveNetworkPolicy(m, cfg)
	if err == nil || !strings.Contains(err.Error(), "network_allow_hosts") {
		t.Fatalf("err=%v", err)
	}
}

func TestValidateAllowedPaths_requiresAbsolute(t *testing.T) {
	t.Parallel()
	_, err := validateAllowedPaths(map[string]string{"data": "/abs"})
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("err=%v", err)
	}
}

func TestValidateAllowedPaths_ok(t *testing.T) {
	t.Parallel()
	out, err := validateAllowedPaths(map[string]string{"/tmp/guest": "/tmp/host"})
	if err != nil {
		t.Fatal(err)
	}
	if out["/tmp/guest"] != "/tmp/host" {
		t.Fatalf("%v", out)
	}
}
