package cloud_test

import (
	"context"
	"testing"

	"github.com/shareed2k/honey/internal/cuetry/secrets/cloud"
)

func TestAWSSM_Handles(t *testing.T) {
	b := cloud.NewAWSSM()
	if !b.Handles("aws-sm:my/secret") {
		t.Fatal()
	}
	if b.Handles("env:X") {
		t.Fatal()
	}
}

func TestAWSSM_Resolve_emptyID(t *testing.T) {
	b := cloud.NewAWSSM()
	_, err := b.Resolve(context.Background(), "aws-sm:")
	if err == nil {
		t.Fatal("expected error for empty id")
	}
}

func TestAWSKMS_Handles(t *testing.T) {
	b := cloud.NewAWSKMS()
	if !b.Handles("aws-kms:abc") {
		t.Fatal()
	}
}

func TestAWSKMS_Resolve_emptyCiphertext(t *testing.T) {
	b := cloud.NewAWSKMS()
	_, err := b.Resolve(context.Background(), "aws-kms:")
	if err == nil {
		t.Fatal()
	}
}

func TestVault_Handles(t *testing.T) {
	b := cloud.NewVault()
	if !b.Handles("vault:secret/data/foo#bar") {
		t.Fatal()
	}
}

func TestVault_Resolve_emptyPath(t *testing.T) {
	b := cloud.NewVault()
	_, err := b.Resolve(context.Background(), "vault:")
	if err == nil {
		t.Fatal("expected error")
	}
}
