package awsprovider

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/shareed2k/honey/internal/hosts"
)

func TestInstanceToRecordPrivatePrimaryWithPublicExtra(t *testing.T) {
	t.Parallel()
	pub := "203.0.113.10"
	priv := "10.0.1.7"
	inst := types.Instance{
		InstanceId:       aws.String("i-abc"),
		PublicIpAddress:  &pub,
		PrivateIpAddress: &priv,
		State:            &types.InstanceState{Name: types.InstanceStateNameRunning},
		Tags:             []types.Tag{{Key: aws.String("Name"), Value: aws.String("web")}},
		Placement:        &types.Placement{AvailabilityZone: aws.String("us-east-1a")},
	}
	rec, ok, err := instanceToRecord(inst, hosts.Query{})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if rec.PrimaryIP != priv {
		t.Fatalf("primary: want %q got %q", priv, rec.PrimaryIP)
	}
	if len(rec.ExtraIPs) != 1 || rec.ExtraIPs[0] != pub {
		t.Fatalf("extras: want [%q] got %#v", pub, rec.ExtraIPs)
	}
}

func TestInstanceToRecordPublicOnlyWhenNoPrivate(t *testing.T) {
	t.Parallel()
	pub := "203.0.113.11"
	inst := types.Instance{
		InstanceId:      aws.String("i-def"),
		PublicIpAddress: &pub,
		State:           &types.InstanceState{Name: types.InstanceStateNameRunning},
		Tags:            []types.Tag{{Key: aws.String("Name"), Value: aws.String("edge")}},
		Placement:       &types.Placement{AvailabilityZone: aws.String("us-east-1b")},
	}
	rec, ok, err := instanceToRecord(inst, hosts.Query{})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if rec.PrimaryIP != pub {
		t.Fatalf("primary: want %q got %q", pub, rec.PrimaryIP)
	}
	if len(rec.ExtraIPs) != 0 {
		t.Fatalf("extras: want empty got %#v", rec.ExtraIPs)
	}
}
