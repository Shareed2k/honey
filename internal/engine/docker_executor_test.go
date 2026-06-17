package engine

import (
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/dockerprovider"
)

// TestVMRecordForHoneyDockerDiscoverRow ...
func TestVMRecordForHoneyDockerDiscoverRow(t *testing.T) {
	rec := hosts.Record{
		Provider: "docker",
		Meta: map[string]string{
			"kind":             "container",
			"container_id":     "abc123",
			"docker_transport": "honey_ssh",
			"docker_discover":  "1",
			"docker_vm":        "stg2-redash-3ccx",
			"docker_vm_ip":     "10.201.0.25",
			"via_provider":     "gcp",
			"docker_run_as":    "root",
			"docker_ssh_user":  "ubuntu",
		},
	}
	if strings.TrimSpace(rec.Meta["docker_backend"]) != "" {
		t.Fatal("discover fixture should use empty docker_backend")
	}
	vm, ok := dockerprovider.VMRecordForHoneyDocker(rec)
	if !ok {
		t.Fatal("expected VM hop for discover row")
	}
	if vm.PrimaryIP != "10.201.0.25" || vm.Name != "stg2-redash-3ccx" {
		t.Fatalf("vm = %#v", vm)
	}
}

// TestEffectiveDockerSSHUser ...
func TestEffectiveDockerSSHUser(t *testing.T) {
	rec := hosts.Record{Meta: map[string]string{"docker_ssh_user": "ubuntu"}}
	if got := dockerprovider.EffectiveDockerSSHUser("deploy", rec); got != "deploy" {
		t.Fatalf("explicit user = %q, want deploy", got)
	}
	if got := dockerprovider.EffectiveDockerSSHUser("", rec); got != "ubuntu" {
		t.Fatalf("meta fallback = %q, want ubuntu", got)
	}
	if got := dockerprovider.EffectiveDockerSSHUser("  ", hosts.Record{}); got != "" {
		t.Fatalf("empty = %q", got)
	}
}
