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
	out := AnsibleList(recs, "deploy", false, nil)
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
	out := AnsibleList(recs, "", false, nil)
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
	hv, err := AnsibleHostVars(recs, "u", "n1", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if hv["ansible_host"] != "1.2.3.4" {
		t.Errorf("expected ansible_host 1.2.3.4, got %v", hv["ansible_host"])
	}

	_, err = AnsibleHostVars(recs, "u", "missing", false, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func checkGroup(t *testing.T, out map[string]any, groupName string) {
	t.Helper()
	if val, ok := out[groupName].(map[string]any); !ok {
		t.Errorf("expected %s group, got nil", groupName)
	} else if hosts, ok := val["hosts"].([]string); !ok || len(hosts) != 1 || hosts[0] != "web-01" {
		t.Errorf("expected %s group with web-01, got %v", groupName, hosts)
	}
}

func TestAnsibleList_stripPrefix(t *testing.T) {
	recs := []hosts.Record{
		{
			Provider:  "gcp",
			Name:      "web-01",
			PrimaryIP: "1.2.3.4",
			Zone:      "us-east1-b",
			Region:    "us-east1",
			Meta: map[string]string{
				"tags":          "webserver,db",
				"label_bg_role": "kafka-main-controller",
				"label_env":     "production",
			},
		},
	}

	out := AnsibleList(recs, "deploy", true, nil)

	// assert properties directly
	checkGroup(t, out, "gcp")
	checkGroup(t, out, "us_east1_b")
	checkGroup(t, out, "us_east1")
	checkGroup(t, out, "webserver")
	checkGroup(t, out, "db")
	checkGroup(t, out, "kafka_main_controller")
	checkGroup(t, out, "production")

	// Should NOT have honey prefixes
	if _, ok := out["honey_provider_gcp"]; ok {
		t.Errorf("expected no honey_provider_gcp group")
	}
	if _, ok := out["honey_tag_webserver"]; ok {
		t.Errorf("expected no honey_tag_webserver group")
	}
	if _, ok := out["honey_label_bg_role_kafka_main_controller"]; ok {
		t.Errorf("expected no honey_label_bg_role_kafka_main_controller group")
	}

	hv, err := AnsibleHostVars(recs, "deploy", "web-01", true, nil)
	if err != nil {
		t.Fatal(err)
	}

	if hv["provider"] != "gcp" {
		t.Errorf("expected provider gcp, got %v", hv["provider"])
	}
	if hv["label_bg_role"] != "kafka-main-controller" {
		t.Errorf("expected label_bg_role kafka-main-controller, got %v", hv["label_bg_role"])
	}
	if hv["tags"] != "webserver,db" {
		t.Errorf("expected tags webserver,db, got %v", hv["tags"])
	}

	// Native ansible variables should remain
	if hv["ansible_host"] != "1.2.3.4" {
		t.Errorf("expected ansible_host 1.2.3.4, got %v", hv["ansible_host"])
	}
}

func TestAnsibleList_blacklist(t *testing.T) {
	recs := []hosts.Record{
		{
			Provider:  "gcp",
			Name:      "web-01",
			PrimaryIP: "1.2.3.4",
			Zone:      "us-east1-b",
			Region:    "us-east1",
			Meta: map[string]string{
				"tags":          "webserver,db,cache",
				"label_bg_role": "kafka",
				"label_env":     "production",
			},
		},
	}

	blacklist := []string{"db", "env", "label_bg_role"}
	out := AnsibleList(recs, "deploy", false, blacklist)

	// Tags
	checkGroup(t, out, "honey_tag_webserver")
	checkGroup(t, out, "honey_tag_cache")
	if _, ok := out["honey_tag_db"]; ok {
		t.Errorf("expected no honey_tag_db group")
	}

	// Labels
	if _, ok := out["honey_label_bg_role_kafka"]; ok {
		t.Errorf("expected no honey_label_bg_role_kafka group")
	}
	if _, ok := out["honey_label_env_production"]; ok {
		t.Errorf("expected no honey_label_env_production group")
	}

	hv, err := AnsibleHostVars(recs, "deploy", "web-01", false, blacklist)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := hv["honey_meta_label_bg_role"]; ok {
		t.Errorf("expected no honey_meta_label_bg_role hostvar")
	}
	if _, ok := hv["honey_meta_label_env"]; ok {
		t.Errorf("expected no honey_meta_label_env hostvar")
	}
	if hv["honey_meta_tags"] != "webserver,cache" {
		t.Errorf("expected tags webserver,cache, got %v", hv["honey_meta_tags"])
	}
}

func TestAnsibleList_PortsStringUnpacking(t *testing.T) {
	recs := []hosts.Record{
		{
			Provider:  "k8s",
			Name:      "test-pod",
			PrimaryIP: "1.2.3.4",
			Meta: map[string]string{
				"ports": `80,443`,
			},
		},
	}

	hv, err := AnsibleHostVars(recs, "deploy", "test-pod", false, nil)
	if err != nil {
		t.Fatal(err)
	}

	portsVal, ok := hv["honey_meta_ports"]
	if !ok {
		t.Fatalf("expected honey_meta_ports to be present")
	}

	portsSlice, ok := portsVal.([]string)
	if !ok {
		t.Fatalf("expected honey_meta_ports to be a []string slice, got %T", portsVal)
	}

	if len(portsSlice) != 2 || portsSlice[0] != "80" || portsSlice[1] != "443" {
		t.Errorf("unexpected ports slice content: %v", portsSlice)
	}
}

func TestAnsibleList_matrixGroups(t *testing.T) {
	recs := []hosts.Record{
		{
			Provider:  "gcp",
			Name:      "web-01",
			PrimaryIP: "1.2.3.4",
			Meta: map[string]string{
				"backend_name":  "mybackend",
				"tags":          "webserver",
				"label_bg_role": "kafka-main",
			},
		},
	}

	out := AnsibleList(recs, "deploy", true, nil)

	// Expected groups when stripPrefix is true:
	expectedGroups := []string{
		"gcp",                      // Provider
		"mybackend",                // Backend
		"gcp_mybackend",            // Provider + Backend
		"webserver",                // Tag
		"mybackend_webserver",      // Backend + Tag
		"gcp_webserver",            // Provider + Tag
		"gcp_mybackend_webserver",  // Provider + Backend + Tag
		"kafka_main",               // Label
		"mybackend_kafka_main",     // Backend + Label
		"gcp_kafka_main",           // Provider + Label
		"gcp_mybackend_kafka_main", // Provider + Backend + Label
	}

	for _, g := range expectedGroups {
		checkGroup(t, out, g)
	}
}
