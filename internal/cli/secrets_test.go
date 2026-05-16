package cli

import (
	"bytes"
	"context"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/cuetry/secrets"
)

func TestSecretsSealUnseal_roundTrip_dataKeyHex(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 3)
	}
	hexKey := hex.EncodeToString(key)

	oldHex := flagSecretsDataKeyHex
	oldFile := flagSecretsFile
	oldCue := flagSecretsCueKey
	defer func() {
		flagSecretsDataKeyHex = oldHex
		flagSecretsFile = oldFile
		flagSecretsCueKey = oldCue
	}()
	flagSecretsDataKeyHex = hexKey
	flagSecretsFile = ""
	flagSecretsCueKey = ""

	var sealOut bytes.Buffer
	secretsSealCmd.SetOut(&sealOut)
	secretsSealCmd.SetErr(&bytes.Buffer{})
	if err := runSecretsSeal(secretsSealCmd, []string{"hello-secret"}); err != nil {
		t.Fatal(err)
	}
	ref := strings.TrimSpace(sealOut.String())
	if !strings.HasPrefix(ref, "secure:v1:") {
		t.Fatalf("ref %q", ref)
	}

	var unsealOut bytes.Buffer
	secretsUnsealCmd.SetOut(&unsealOut)
	secretsUnsealCmd.SetErr(&bytes.Buffer{})
	if err := runSecretsUnseal(secretsUnsealCmd, []string{ref}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSuffix(unsealOut.String(), "\n") != "hello-secret" {
		t.Fatalf("got %q", unsealOut.String())
	}
}

func TestSecretsSeal_cueKey(t *testing.T) {
	key := bytes.Repeat([]byte{0x01}, 32)
	oldHex := flagSecretsDataKeyHex
	oldCue := flagSecretsCueKey
	defer func() {
		flagSecretsDataKeyHex = oldHex
		flagSecretsCueKey = oldCue
	}()
	flagSecretsDataKeyHex = hex.EncodeToString(key)
	flagSecretsCueKey = "DB_PASSWORD"

	var out bytes.Buffer
	secretsSealCmd.SetOut(&out)
	if err := runSecretsSeal(secretsSealCmd, []string{"x"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "DB_PASSWORD: \"secure:v1:") {
		t.Fatalf("got %q", out.String())
	}
}

func TestKeyringInitDataKey_importHex(t *testing.T) {
	key := bytes.Repeat([]byte{0xab}, 32)
	old := flagKeyringInitDataKeyHex
	defer func() { flagKeyringInitDataKeyHex = old }()
	flagKeyringInitDataKeyHex = hex.EncodeToString(key)
	got, err := keyringInitDataKey()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(key) {
		t.Fatal("mismatch")
	}
}

func TestKeyringInitDataKey_generate(t *testing.T) {
	oldHex := flagKeyringInitDataKeyHex
	oldFile := flagKeyringInitDataKeyFile
	defer func() {
		flagKeyringInitDataKeyHex = oldHex
		flagKeyringInitDataKeyFile = oldFile
	}()
	flagKeyringInitDataKeyHex = ""
	flagKeyringInitDataKeyFile = ""
	k1, err := keyringInitDataKey()
	if err != nil {
		t.Fatal(err)
	}
	k2, err := keyringInitDataKey()
	if err != nil {
		t.Fatal(err)
	}
	if len(k1) != 32 || len(k2) != 32 || string(k1) == string(k2) {
		t.Fatal("expected two random 32-byte keys")
	}
}

func TestSecretsPackage_roundTrip(t *testing.T) {
	key := make([]byte, 32)
	opts := secrets.Options{SymmetricDataKey: key}
	ref, err := secrets.Seal(context.Background(), opts, "pw")
	if err != nil {
		t.Fatal(err)
	}
	got, err := secrets.Unseal(context.Background(), opts, ref)
	if err != nil {
		t.Fatal(err)
	}
	if got != "pw" {
		t.Fatalf("got %q", got)
	}
}
