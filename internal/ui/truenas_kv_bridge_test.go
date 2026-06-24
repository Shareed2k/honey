package ui

import (
	"bufio"
	"bytes"
	"testing"

	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hosts"
)

func TestTruenasApplianceSSHForwardEligible(t *testing.T) {
	if !engine.TruenasApplianceSSHForwardEligible(hosts.Record{
		Provider: "truenas", PrimaryIP: "10.0.0.5", Meta: map[string]string{"kind": "appliance"},
	}) {
		t.Fatal("expected appliance+ip eligible")
	}
	if engine.TruenasApplianceSSHForwardEligible(hosts.Record{
		Provider: "truenas", Meta: map[string]string{"kind": "virt_instance", "id": "x"},
	}) {
		t.Fatal("guest should not use ssh forward path")
	}
}

func TestReadTrueNASBridgeReady(t *testing.T) {
	br := bufio.NewReader(bytes.NewBufferString("READY 8765\n"))
	port, err := engine.ReadTrueNASBridgeReady(br)
	if err != nil || port != 8765 {
		t.Fatalf("port=%d err=%v", port, err)
	}
	br2 := bufio.NewReader(bytes.NewBufferString("noise\nREADY 9999\n"))
	port2, err2 := engine.ReadTrueNASBridgeReady(br2)
	if err2 != nil || port2 != 9999 {
		t.Fatalf("port=%d err=%v", port2, err2)
	}
}

func TestTruenasEnterUsesAPIForExec(t *testing.T) {
	r := hosts.Record{Provider: "truenas", Meta: map[string]string{"kind": "vm", "virt_instance_id": "id"}}
	if tableEnterAction(r) != actTrueNASAPI {
		t.Fatal("expected API action for truenas enter")
	}
	if !r.IsConnectable() {
		t.Fatal("expected connectable without IP")
	}
}
