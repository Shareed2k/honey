package dockerprovider

import (
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestVMNodeMetaExternalIP(t *testing.T) {
	vm := hosts.Record{
		Provider:  "gcp",
		Name:      "vm-1",
		PrimaryIP: "10.0.0.5",
		ExtraIPs:  []string{"34.76.1.2"},
		Meta:      map[string]string{"backend_name": "gcp-stg2"},
	}
	meta := vmNodeMeta(vm, true)
	if meta["docker_vm_ip"] != "10.0.0.5" {
		t.Fatalf("docker_vm_ip = %q", meta["docker_vm_ip"])
	}
	if meta["docker_vm_external_ip"] != "34.76.1.2" {
		t.Fatalf("docker_vm_external_ip = %q", meta["docker_vm_external_ip"])
	}
	if meta["docker_discover_vm_external_ip"] != "34.76.1.2" {
		t.Fatalf("docker_discover_vm_external_ip = %q", meta["docker_discover_vm_external_ip"])
	}

	var rec hosts.Record
	applyVMNodeRecordFields(&rec, vm)
	if rec.PrimaryIP != "34.76.1.2" {
		t.Fatalf("PrimaryIP = %q", rec.PrimaryIP)
	}
	if len(rec.ExtraIPs) != 1 || rec.ExtraIPs[0] != "10.0.0.5" {
		t.Fatalf("ExtraIPs = %#v", rec.ExtraIPs)
	}
}
