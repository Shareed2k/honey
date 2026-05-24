package plugins

import (
	"testing"

	"github.com/shareed2k/honey/internal/config"
)

func TestRcloneManifest_networkPolicy(t *testing.T) {
	t.Parallel()
	m := Manifest{
		ID:           "rclone",
		Capabilities: []string{CapCustomStep, CapSecret},
		AllowedHosts: []string{"127.0.0.1"},
	}
	cfg := config.PluginsEffective{NetworkAllowHosts: []string{"127.0.0.1"}}
	hosts, _, err := validateManifestPolicy(m, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || hosts[0] != "127.0.0.1" {
		t.Fatalf("hosts=%v", hosts)
	}
}

func TestRcloneManifest_rejectsNonLoopbackWhenCapSet(t *testing.T) {
	t.Parallel()
	m := Manifest{ID: "rclone", AllowedHosts: []string{"rcd.example.com"}}
	cfg := config.PluginsEffective{NetworkAllowHosts: []string{"127.0.0.1"}}
	_, _, err := validateManifestPolicy(m, cfg)
	if err == nil {
		t.Fatal("expected network_allow_hosts mismatch error")
	}
}
