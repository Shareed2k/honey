package webserver

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"testing"
	"time"
)

// TestDeviceCASignGroups proves that Sign records the supplied groups in the
// issued certificate's Subject Organization (O=) — the field honey's access
// gateways read back as the impersonated groups.
func TestDeviceCASignGroups(t *testing.T) {
	ca, err := LoadOrCreateDeviceCA(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreateDeviceCA: %v", err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader,
		&x509.CertificateRequest{Subject: pkix.Name{CommonName: "alice"}}, key)
	if err != nil {
		t.Fatalf("CreateCertificateRequest: %v", err)
	}
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		t.Fatalf("ParseCertificateRequest: %v", err)
	}

	pemBytes, err := ca.Sign(csr, "alice@corp", []string{"developers", "eng"}, time.Hour)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		t.Fatalf("no PEM block in issued cert")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	if cert.Subject.CommonName != "alice@corp" {
		t.Errorf("CN = %q, want alice@corp", cert.Subject.CommonName)
	}
	// Order is not preserved: x509 encodes Organization as a pkix SET, which the
	// encoder may reorder. Groups are a set, so compare membership.
	got := map[string]bool{}
	for _, o := range cert.Subject.Organization {
		got[o] = true
	}
	if len(got) != 2 || !got["developers"] || !got["eng"] {
		t.Errorf("Organization = %v, want the set {developers, eng}", cert.Subject.Organization)
	}
}

// TestDeviceCASignNoGroups proves that nil groups leave Organization empty
// (the enrollment path passes nil — no behavior change).
func TestDeviceCASignNoGroups(t *testing.T) {
	ca, err := LoadOrCreateDeviceCA(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreateDeviceCA: %v", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader,
		&x509.CertificateRequest{Subject: pkix.Name{CommonName: "device-1"}}, key)
	if err != nil {
		t.Fatalf("CreateCertificateRequest: %v", err)
	}
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		t.Fatalf("ParseCertificateRequest: %v", err)
	}
	pemBytes, err := ca.Sign(csr, "device-1", nil, time.Hour)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	block, _ := pem.Decode(pemBytes)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	if len(cert.Subject.Organization) != 0 {
		t.Errorf("Organization = %v, want empty", cert.Subject.Organization)
	}
}
