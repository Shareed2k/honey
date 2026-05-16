package stack

import (
	"testing"
)

func TestEncryptDecryptSymmetricV1_roundTrip(t *testing.T) {
	key := make([]byte, SymmetricKeyBytes)
	for i := range key {
		key[i] = byte(i)
	}
	inner, err := EncryptSymmetricV1(key, "hello-secret")
	if err != nil {
		t.Fatal(err)
	}
	ref := "secure:" + inner
	if err := ValidateSecureRef(ref); err != nil {
		t.Fatal(err)
	}
	got, err := DecryptSymmetricV1(key, inner)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello-secret" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatSecureRef(t *testing.T) {
	key := make([]byte, SymmetricKeyBytes)
	ref, err := FormatSecureRef(key, "x")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSecureRef(ref); err != nil {
		t.Fatal(err)
	}
}
