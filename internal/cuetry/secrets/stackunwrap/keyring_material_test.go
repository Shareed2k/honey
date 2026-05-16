package stackunwrap

import (
	"testing"
)

func TestEncodeDecodeKeyringMaterial_roundTrip(t *testing.T) {
	key := make([]byte, keyringStackKeyBytes)
	for i := range key {
		key[i] = byte(i + 7)
	}
	enc, err := EncodeKeyringMaterial(key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeKeyringMaterial(enc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(key) {
		t.Fatal("round-trip mismatch")
	}
}
