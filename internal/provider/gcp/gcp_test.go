package gcp

import (
	"testing"

	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/protobuf/proto"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestInstanceToRecordInternalPrimaryNatExtra(t *testing.T) {
	t.Parallel()
	inst := &computepb.Instance{
		Name:   proto.String("vm-a"),
		Status: proto.String("RUNNING"),
		Id:     proto.Uint64(99),
		NetworkInterfaces: []*computepb.NetworkInterface{
			{
				NetworkIP: proto.String("10.2.3.4"),
				AccessConfigs: []*computepb.AccessConfig{
					{NatIP: proto.String("203.0.113.20")},
				},
			},
		},
		Zone: proto.String("zones/us-central1-a"),
	}
	rec, ok, err := instanceToRecord(inst, hosts.Query{})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if rec.PrimaryIP != "10.2.3.4" {
		t.Fatalf("primary: want 10.2.3.4 got %q", rec.PrimaryIP)
	}
	if len(rec.ExtraIPs) != 1 || rec.ExtraIPs[0] != "203.0.113.20" {
		t.Fatalf("extras: want [203.0.113.20] got %#v", rec.ExtraIPs)
	}
}

func TestInstanceToRecordNatOnlyWhenNoInternal(t *testing.T) {
	t.Parallel()
	inst := &computepb.Instance{
		Name:   proto.String("vm-b"),
		Status: proto.String("RUNNING"),
		Id:     proto.Uint64(100),
		NetworkInterfaces: []*computepb.NetworkInterface{
			{
				AccessConfigs: []*computepb.AccessConfig{
					{NatIP: proto.String("203.0.113.21")},
				},
			},
		},
		Zone: proto.String("zones/us-central1-b"),
	}
	rec, ok, err := instanceToRecord(inst, hosts.Query{})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if rec.PrimaryIP != "203.0.113.21" {
		t.Fatalf("primary: want 203.0.113.21 got %q", rec.PrimaryIP)
	}
	if len(rec.ExtraIPs) != 0 {
		t.Fatalf("extras: want empty got %#v", rec.ExtraIPs)
	}
}
