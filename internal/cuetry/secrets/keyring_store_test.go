package secrets

import (
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/cuetry/secrets/stack"
	"github.com/shareed2k/honey/internal/cuetry/secrets/stackunwrap"
)

func TestKeyringProviderURL(t *testing.T) {
	if got := KeyringProviderURL("honey", "stack-data-key"); got != "keyring://honey/stack-data-key" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatKeyringStackKeyValue_roundTrip(t *testing.T) {
	key := make([]byte, stack.SymmetricKeyBytes)
	val, err := FormatKeyringStackKeyValue(key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := stackunwrap.DecodeKeyringMaterial(val)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != stack.SymmetricKeyBytes {
		t.Fatalf("got %d bytes", len(got))
	}
}

func TestGenerateStackDataKey(t *testing.T) {
	k1, err := GenerateStackDataKey()
	if err != nil {
		t.Fatal(err)
	}
	k2, err := GenerateStackDataKey()
	if err != nil {
		t.Fatal(err)
	}
	if string(k1) == string(k2) {
		t.Fatal("expected distinct random keys")
	}
}

func TestKeyringConfigSnippet(t *testing.T) {
	s := KeyringConfigSnippet("keyring://honey/stack-data-key")
	if !strings.Contains(s, "secretsprovider: keyring://honey/stack-data-key") {
		t.Fatalf("%q", s)
	}
}
