package devmtls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeSigner mimics the off-process signer (Android Keystore) using an in-memory
// ECDSA key: signs the digest, returns ASN.1 DER — exactly what Go's TLS expects.
type fakeSigner struct{ key *ecdsa.PrivateKey }

func (f fakeSigner) Sign(digest []byte) ([]byte, error) {
	return ecdsa.SignASN1(rand.Reader, f.key, digest)
}

func TestClientTLSConfig_HandshakeViaCallbackSigner(t *testing.T) {
	t.Cleanup(Clear)

	// One CA signs both the server cert and the client cert.
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true, IsCA: true,
	}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	caCert, _ := x509.ParseCertificate(caDER)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	issue := func(cn string, eku x509.ExtKeyUsage, ips []net.IP) (*ecdsa.PrivateKey, []byte) {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{eku}, IPAddresses: ips,
		}
		der, _ := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
		return key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	}

	serverKey, serverCertPEM := issue("localhost", x509.ExtKeyUsageServerAuth, []net.IP{net.IPv4(127, 0, 0, 1)})
	clientKey, clientCertPEM := issue("device:test", x509.ExtKeyUsageClientAuth, nil)

	// Register the client credential with a callback signer (no raw key handed to Go's config).
	Set(clientCertPEM, caPEM, fakeSigner{key: clientKey})

	// mTLS server requiring a client cert signed by the CA.
	srvKeyDER, _ := x509.MarshalECPrivateKey(serverKey)
	srvPair, err := tls.X509KeyPair(serverCertPEM, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: srvKeyDER}))
	if err != nil {
		t.Fatal(err)
	}
	clientPool := x509.NewCertPool()
	clientPool.AppendCertsFromPEM(caPEM)

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cn := ""
		if len(r.TLS.PeerCertificates) > 0 {
			cn = r.TLS.PeerCertificates[0].Subject.CommonName
		}
		_, _ = io.WriteString(w, cn)
	}))
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{srvPair},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientPool,
	}
	srv.StartTLS()
	defer srv.Close()

	cfg, err := ClientTLSConfig("")
	if err != nil {
		t.Fatalf("ClientTLSConfig: %v", err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("mTLS GET (callback signer path): %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if string(body) != "device:test" {
		t.Fatalf("server saw client CN %q, want device:test", string(body))
	}
}

func TestClientTLSConfig_NotRegistered(t *testing.T) {
	t.Cleanup(Clear)
	Clear()
	if Registered() {
		t.Fatal("expected not registered")
	}
	if _, err := ClientTLSConfig(""); err == nil {
		t.Fatal("expected error when unregistered")
	}
}
