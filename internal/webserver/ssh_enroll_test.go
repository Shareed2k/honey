package webserver

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/sshca"
)

func testSSHEnrollAPI(t *testing.T) *SSHEnrollAPI {
	t.Helper()
	ca, err := sshca.LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreateCA: %v", err)
	}
	return NewSSHEnrollAPI(ca)
}

// newSSHPubKey returns a freshly generated ed25519 SSH public key as an
// authorized_keys line, plus its parsed form for trust comparisons.
func newSSHPubKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return string(ssh.MarshalAuthorizedKey(sshPub))
}

func mintSSHCode(t *testing.T, a *SSHEnrollAPI, reqBody map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(reqBody)
	rec := httptest.NewRecorder()
	a.handleMintSSHEnrollCode(rec, httptest.NewRequest(http.MethodPost, "/api/v1/ssh/enroll-code", strings.NewReader(string(body))))
	return rec
}

func sshEnroll(t *testing.T, a *SSHEnrollAPI, code, pubKey string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"code": code, "public_key": pubKey})
	rec := httptest.NewRecorder()
	a.handleSSHEnroll(rec, httptest.NewRequest(http.MethodPost, "/api/v1/ssh/enroll", strings.NewReader(string(body))))
	return rec
}

func TestSSHEnrollFlow(t *testing.T) {
	t.Parallel()
	a := testSSHEnrollAPI(t)

	rec := mintSSHCode(t, a, map[string]any{"principals": []string{"alice"}, "key_id": "alice", "ttl": "1h"})
	if rec.Code != http.StatusOK {
		t.Fatalf("mint: got %d, body %s", rec.Code, rec.Body.String())
	}
	var mint struct {
		Code             string `json:"code"`
		ExpiresInSeconds int    `json:"expires_in_seconds"`
		CA               string `json:"ca"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &mint); err != nil {
		t.Fatal(err)
	}
	if mint.Code == "" {
		t.Fatal("mint: empty code")
	}
	if mint.ExpiresInSeconds != int(enrollCodeTTL.Seconds()) {
		t.Fatalf("expires_in_seconds = %d, want %d", mint.ExpiresInSeconds, int(enrollCodeTTL.Seconds()))
	}
	if strings.TrimSpace(mint.CA) == "" {
		t.Fatal("mint: empty ca")
	}

	rec = sshEnroll(t, a, mint.Code, newSSHPubKey(t))
	if rec.Code != http.StatusOK {
		t.Fatalf("enroll: got %d, body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Cert            string   `json:"cert"`
		CA              string   `json:"ca"`
		Principals      []string `json:"principals"`
		ValidBeforeUnix uint64   `json:"valid_before_unix"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}

	// The returned cert parses as an *ssh.Certificate.
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(out.Cert))
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		t.Fatalf("issued key is %T, want *ssh.Certificate", pub)
	}

	// A CertChecker that trusts this CA accepts the granted principal.
	caPub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(out.CA))
	if err != nil {
		t.Fatalf("parse ca: %v", err)
	}
	checker := &ssh.CertChecker{
		IsUserAuthority: func(auth ssh.PublicKey) bool {
			return bytes.Equal(auth.Marshal(), caPub.Marshal())
		},
	}
	if err := checker.CheckCert("alice", cert); err != nil {
		t.Fatalf("CheckCert(alice): %v", err)
	}
	// A principal that was not granted must be rejected.
	if err := checker.CheckCert("bob", cert); err == nil {
		t.Fatal("CheckCert(bob) succeeded, want failure")
	}
	if len(out.Principals) != 1 || out.Principals[0] != "alice" {
		t.Fatalf("principals = %v, want [alice]", out.Principals)
	}
	if out.ValidBeforeUnix == 0 {
		t.Fatal("valid_before_unix = 0")
	}
}

func TestSSHEnrollCodeSingleUse(t *testing.T) {
	t.Parallel()
	a := testSSHEnrollAPI(t)

	rec := mintSSHCode(t, a, map[string]any{"principal": "alice"})
	if rec.Code != http.StatusOK {
		t.Fatalf("mint: got %d", rec.Code)
	}
	var mint struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &mint); err != nil {
		t.Fatal(err)
	}

	if rec := sshEnroll(t, a, mint.Code, newSSHPubKey(t)); rec.Code != http.StatusOK {
		t.Fatalf("first enroll: got %d, body %s", rec.Code, rec.Body.String())
	}
	// Same code again → rejected (single use).
	if rec := sshEnroll(t, a, mint.Code, newSSHPubKey(t)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("reused code: got %d, want 401", rec.Code)
	}
}

func TestSSHEnrollBadCode(t *testing.T) {
	t.Parallel()
	a := testSSHEnrollAPI(t)
	if rec := sshEnroll(t, a, "nope", newSSHPubKey(t)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad code: got %d, want 401", rec.Code)
	}
}

func TestSSHEnrollDisabled(t *testing.T) {
	t.Parallel()
	a := NewSSHEnrollAPI(nil) // disabled: nil CA

	tests := []struct {
		name    string
		handler http.HandlerFunc
		path    string
	}{
		{"mint", a.handleMintSSHEnrollCode, "/api/v1/ssh/enroll-code"},
		{"enroll", a.handleSSHEnroll, "/api/v1/ssh/enroll"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			tt.handler(rec, httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(`{}`)))
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("%s: got %d, want 503", tt.name, rec.Code)
			}
		})
	}
}

func TestSSHEnrollMintValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body map[string]any
		want int
	}{
		{"single principal string", map[string]any{"principal": "alice"}, http.StatusOK},
		{"principals array", map[string]any{"principals": []string{"alice", "ops"}}, http.StatusOK},
		{"default ttl", map[string]any{"principal": "alice"}, http.StatusOK},
		{"no principal", map[string]any{}, http.StatusBadRequest},
		{"blank principal", map[string]any{"principal": "   "}, http.StatusBadRequest},
		{"bad ttl", map[string]any{"principal": "alice", "ttl": "nonsense"}, http.StatusBadRequest},
		{"non-positive ttl", map[string]any{"principal": "alice", "ttl": "0s"}, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := testSSHEnrollAPI(t)
			rec := mintSSHCode(t, a, tt.body)
			if rec.Code != tt.want {
				t.Fatalf("got %d, want %d (body %s)", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}
