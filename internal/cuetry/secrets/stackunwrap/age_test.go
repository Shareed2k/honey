package stackunwrap

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
	"filippo.io/age/armor"
)

func TestAge_Unwrap_inlineCiphertext(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	idPath := filepath.Join(dir, "id.txt")
	if err := os.WriteFile(idPath, []byte(id.String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0xab}, 32)
	var buf bytes.Buffer
	aw := armor.NewWriter(&buf)
	w, err := age.Encrypt(aw, id.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(key); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := aw.Close(); err != nil {
		t.Fatal(err)
	}
	u := Age{IdentityFile: idPath}
	got, err := u.Unwrap(context.Background(), "age://", buf.String())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, key) {
		t.Fatalf("got %d bytes", len(got))
	}
}
