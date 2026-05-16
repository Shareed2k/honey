package secrets

import (
	"context"
	"testing"
)

func TestResolveStackDataKey_static(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	got, err := ResolveStackDataKey(context.Background(), Options{SymmetricDataKey: key})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(key) {
		t.Fatal("key mismatch")
	}
	got[0] = 0xff
	if key[0] == 0xff {
		t.Fatal("returned slice aliases input")
	}
}

func TestResolveStackDataKey_missingConfig(t *testing.T) {
	_, err := ResolveStackDataKey(context.Background(), Options{})
	if err == nil {
		t.Fatal("expected error")
	}
}
