package inventory

import (
	"testing"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
)

func TestApplyMergesGlobalGroupsAndHostVars(t *testing.T) {
	t.Parallel()
	records := []hosts.Record{{
		Provider:  "local",
		Name:      "web-01",
		PrimaryIP: "10.0.1.10",
		Meta:      map[string]string{"env": "prod", "role": "web"},
	}}
	inv := config.Inventory{
		Vars: map[string]config.InventoryValue{
			"service":         config.MustInventoryValue("base"),
			"restart_timeout": config.MustInventoryValue(30),
		},
		Groups: map[string]config.InventoryGroup{
			"prod": {
				Priority: 100,
				Match:    "host.meta['env'] == 'prod'",
				Vars: map[string]config.InventoryValue{
					"deploy_env": config.MustInventoryValue("prod"),
					"service":    config.MustInventoryValue("prod-service"),
				},
			},
			"web": {
				Priority: 200,
				Match:    "host.meta['role'] == 'web'",
				Vars: map[string]config.InventoryValue{
					"service": config.MustInventoryValue("nginx"),
				},
			},
		},
		Hosts: map[string]config.InventoryHost{
			"local/web-01/10.0.1.10": {
				Vars: map[string]config.InventoryValue{
					"restart_timeout": config.MustInventoryValue(10),
				},
			},
		},
	}

	if err := Apply(records, inv); err != nil {
		t.Fatal(err)
	}
	got := records[0]
	if len(got.Groups) != 2 || got.Groups[0] != "prod" || got.Groups[1] != "web" {
		t.Fatalf("groups = %#v", got.Groups)
	}
	if got.Vars["service"].String() != "nginx" {
		t.Fatalf("service = %q", got.Vars["service"].String())
	}
	if got.Vars["restart_timeout"].String() != "10" {
		t.Fatalf("restart_timeout = %q", got.Vars["restart_timeout"].String())
	}
	if got.Vars["deploy_env"].String() != "prod" {
		t.Fatalf("deploy_env = %q", got.Vars["deploy_env"].String())
	}
}
