package webserver

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testEnrollAPI(t *testing.T) *EnrollAPI {
	t.Helper()
	ca, err := LoadOrCreateDeviceCA(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreateDeviceCA: %v", err)
	}
	return NewEnrollAPI(ca, newEnrollStore(), 0)
}

// newTestCSR returns a PEM CSR for CN (the CN is overridden by the server from
// the enrollment code, but ParseCertificateRequest still needs a valid CSR).
func newTestCSR(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader,
		&x509.CertificateRequest{Subject: pkix.Name{CommonName: "ignored"}}, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}

func mintCode(t *testing.T, a *EnrollAPI, cn string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"cn": cn})
	rec := httptest.NewRecorder()
	a.handleMintEnrollCode(rec, httptest.NewRequest(http.MethodPost, "/api/v1/devices/enroll-code", strings.NewReader(string(body))))
	if rec.Code != http.StatusOK {
		t.Fatalf("mint: got %d, body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Code == "" {
		t.Fatal("mint: empty code")
	}
	return out.Code
}

func enroll(t *testing.T, a *EnrollAPI, code, csr string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"code": code, "csr": csr})
	rec := httptest.NewRecorder()
	a.handleDeviceEnroll(rec, httptest.NewRequest(http.MethodPost, "/api/v1/devices/enroll", strings.NewReader(string(body))))
	return rec
}

func TestDeviceEnrollFlow(t *testing.T) {
	a := testEnrollAPI(t)
	code := mintCode(t, a, "device:abc")
	csr := newTestCSR(t)

	rec := enroll(t, a, code, csr)
	if rec.Code != http.StatusOK {
		t.Fatalf("enroll: got %d, body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		CN   string `json:"cn"`
		Cert string `json:"cert"`
		CA   string `json:"ca"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.CN != "device:abc" {
		t.Fatalf("cn = %q, want device:abc", out.CN)
	}

	// Issued cert: parses, CN from the code, client-auth EKU, chains to the CA.
	blk, _ := pem.Decode([]byte(out.Cert))
	if blk == nil {
		t.Fatal("cert not PEM")
	}
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if cert.Subject.CommonName != "device:abc" {
		t.Fatalf("cert CN = %q", cert.Subject.CommonName)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(out.CA)) {
		t.Fatal("append CA")
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("verify against CA: %v", err)
	}

	// A device record was captured.
	if got := len(a.store.list()); got != 1 {
		t.Fatalf("device records = %d, want 1", got)
	}
}

func TestDeviceEnrollCodeSingleUse(t *testing.T) {
	a := testEnrollAPI(t)
	code := mintCode(t, a, "")
	csr := newTestCSR(t)

	if rec := enroll(t, a, code, csr); rec.Code != http.StatusOK {
		t.Fatalf("first enroll: got %d", rec.Code)
	}
	// Same code again → rejected (single use).
	if rec := enroll(t, a, code, csr); rec.Code != http.StatusUnauthorized {
		t.Fatalf("reused code: got %d, want 401", rec.Code)
	}
}

func TestDeviceEnrollBadCode(t *testing.T) {
	a := testEnrollAPI(t)
	if rec := enroll(t, a, "nope", newTestCSR(t)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad code: got %d, want 401", rec.Code)
	}
}

func TestResolveDeviceCertTTL(t *testing.T) {
	if got := resolveDeviceCertTTL(0); got != defaultDeviceCertTTL {
		t.Fatalf("zero → %s, want %s", got, defaultDeviceCertTTL)
	}
	if got := resolveDeviceCertTTL(-time.Minute); got != defaultDeviceCertTTL {
		t.Fatalf("negative → %s, want %s", got, defaultDeviceCertTTL)
	}
	if got := resolveDeviceCertTTL(3 * time.Hour); got != 3*time.Hour {
		t.Fatalf("explicit → %s, want 3h", got)
	}
	if defaultDeviceCertTTL != 12*time.Hour {
		t.Fatalf("defaultDeviceCertTTL = %s, want 12h", defaultDeviceCertTTL)
	}
}

func TestDeviceEnrollUsesConfiguredTTL(t *testing.T) {
	ca, err := LoadOrCreateDeviceCA(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreateDeviceCA: %v", err)
	}
	a := NewEnrollAPI(ca, newEnrollStore(), 2*time.Hour)
	code := mintCode(t, a, "device:ttl")

	before := time.Now()
	rec := enroll(t, a, code, newTestCSR(t))
	if rec.Code != http.StatusOK {
		t.Fatalf("enroll: got %d, body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Cert string `json:"cert"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	blk, _ := pem.Decode([]byte(out.Cert))
	if blk == nil {
		t.Fatal("cert not PEM")
	}
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	// Validity window should be ~2h from issuance, not the old 720h default.
	if got := cert.NotAfter.Sub(before); got > 2*time.Hour+5*time.Minute || got < 2*time.Hour-5*time.Minute {
		t.Fatalf("cert validity = %s, want ~2h", got)
	}
}

func TestDeviceEnrollDisabled(t *testing.T) {
	a := NewEnrollAPI(nil, nil, 0) // disabled: nil CA and store
	rec := httptest.NewRecorder()
	a.handleDeviceEnroll(rec, httptest.NewRequest(http.MethodPost, "/api/v1/devices/enroll", strings.NewReader(`{}`)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled: got %d, want 503", rec.Code)
	}
}
