package inventory

import (
	"encoding/json"
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestAnsibleList_basic(t *testing.T) {
	recs := []hosts.Record{
		{
			Provider:  "gcp",
			Name:      "web-1",
			PrimaryIP: "10.0.0.5",
			Zone:      "us-east1-b",
			Region:    "us-east1",
			Meta:      map[string]string{"env": "prod"},
		},
	}
	out := AnsibleList(recs, "deploy")
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]any
	if err := json.Unmarshal(b, &top); err != nil {
		t.Fatal(err)
	}
	meta, ok := top["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("_meta: %#v", top["_meta"])
	}
	hvmap, ok := meta["hostvars"].(map[string]any)
	if !ok {
		t.Fatalf("hostvars: %#v", meta["hostvars"])
	}
	hv, ok := hvmap["web-1"].(map[string]any)
	if !ok {
		t.Fatalf("host web-1: %#v", hvmap["web-1"])
	}
	if hv["ansible_host"] != "10.0.0.5" {
		t.Fatalf("ansible_host: %v", hv["ansible_host"])
	}
	if hv["ansible_user"] != "deploy" {
		t.Fatalf("ansible_user: %v", hv["ansible_user"])
	}
	if hv["honey_meta_env"] != "prod" {
		t.Fatalf("honey_meta_env: %v", hv["honey_meta_env"])
	}
	grp, ok := top["honey_provider_gcp"].(map[string]any)
	if !ok {
		t.Fatalf("provider group: %#v", top["honey_provider_gcp"])
	}
	hostsRaw, _ := json.Marshal(grp["hosts"])
	if string(hostsRaw) != `["web-1"]` {
		t.Fatalf("provider hosts: %s", hostsRaw)
	}
}

func TestAnsibleList_duplicateNameDifferentProvider(t *testing.T) {
	recs := []hosts.Record{
		{Provider: "gcp", Name: "app", PrimaryIP: "10.0.0.1"},
		{Provider: "aws", Name: "app", PrimaryIP: "10.0.0.2"},
	}
	out := AnsibleList(recs, "")
	meta := out["_meta"].(map[string]any)
	hvmap := meta["hostvars"].(map[string]any)
	if len(hvmap) != 2 {
		t.Fatalf("want 2 hostvars, got %d", len(hvmap))
	}
	if _, ok := hvmap["app"]; !ok {
		t.Fatalf("missing app: %v", hvmap)
	}
	if _, ok := hvmap["app__aws"]; !ok {
		t.Fatalf("missing app__aws: %v", hvmap)
	}
}

func TestAnsibleHostVars(t *testing.T) {
	recs := []hosts.Record{
		{Name: "n1", PrimaryIP: "1.2.3.4", Provider: "k8s"},
	}
	hv, err := AnsibleHostVars(recs, "u", "n1")
	if err != nil {
		t.Fatal(err)
	}
	if hv["ansible_host"] != "1.2.3.4" || hv["ansible_user"] != "u" {
		t.Fatalf("%#v", hv)
	}
	_, err = AnsibleHostVars(recs, "u", "missing")
	if err == nil {
		t.Fatal("expected error")
	}
}
