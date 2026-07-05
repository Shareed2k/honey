//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/devmtls"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/honeyprovider"
	"github.com/shareed2k/honey/internal/provider/localprovider"
	"github.com/shareed2k/honey/internal/searchrun"
	"github.com/shareed2k/honey/internal/webserver"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// e2eSigner mimics the Android Keystore: signs the digest with an in-memory EC
// key, returning ASN.1 DER — what Go's TLS stack expects from a crypto.Signer.
type e2eSigner struct{ key *ecdsa.PrivateKey }

func (s e2eSigner) Sign(digest []byte) ([]byte, error) {
	return ecdsa.SignASN1(rand.Reader, s.key, digest)
}

// TestHoneyProviderMTLS_E2E is the full device-mTLS path: an in-process honey
// server issues a device certificate via its enrollment API; a real gateway
// container terminates mTLS against that device CA and proxies to honey; the
// honeyprovider fetches backends + hosts over the device client certificate, with
// signing delegated to a callback (as on-device the key lives in the TEE).
func TestHoneyProviderMTLS_E2E(t *testing.T) {
	// Isolate the honey device CA under a temp state dir.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Cleanup(devmtls.Clear)

	// 1. In-process honey (no-auth: APISIX is the authenticator) with a local backend.
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	configYAML := `
version: 1
backends:
  local:
    - name: "prod"
      hosts:
        - name: "remote-host"
          primary_ip: "10.0.0.1"
`
	if err := os.WriteFile(configPath, []byte(configYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	remoteSearchReg := searchrun.NewRegistry([]searchrun.ProviderFactory{
		localprovider.NewFactory(testLocalConfig{}),
	})
	// Bind 0.0.0.0 so the APISIX container can reach honey via the host.
	honeyBase, honeyPort := newTestServerOn(t, webserver.Options{
		SearchRegistry: remoteSearchReg,
		DisableAuth:    true,
		ConfigPath:     configPath,
	}, "0.0.0.0")

	// 2. Enroll a device: mint a code, submit a CSR, receive a client cert signed
	//    by honey's device CA. The key is generated locally (stands in for the TEE).
	deviceKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	clientChainPEM, deviceCAPEM := enrollDevice(t, honeyBase, deviceKey, "device:e2e")

	// 3. Gateway server cert (its own CA), trusting the device CA for client certs.
	serverCAPEM, serverCertPEM, serverKeyPEM := genServerCert(t)

	// 4. Start the mTLS gateway in front of honey, requiring the device client cert.
	gatewayURL := startMTLSGateway(t, honeyPort, serverCertPEM, serverKeyPEM, deviceCAPEM)

	// 5. Register the device credential (chain + gateway CA + callback signer).
	devmtls.Set([]byte(clientChainPEM), []byte(serverCAPEM), e2eSigner{key: deviceKey})

	// 6. Drive the honeyprovider over mTLS through the gateway.
	cfg := &config.File{Backends: config.Backends{Honey: []config.HoneyBackend{
		{Name: "remote1", URL: gatewayURL, MTLS: true, ServerCA: serverCAPEM},
	}}}
	factory := honeyprovider.NewFactory(honeyTestConfig{cfg})

	rows := factory.BackendRows()
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows (honey + local) over mTLS, got %d: %+v", len(rows), rows)
	}

	provider := factory.FromConfig(nil)[0]
	recs, err := provider.Search(context.Background(), hosts.Query{NameSubstring: "remote"})
	if err != nil {
		t.Fatalf("mTLS search failed: %v", err)
	}
	if len(recs) != 1 || recs[0].Name != "remote-host" {
		t.Fatalf("unexpected search result over mTLS: %+v", recs)
	}

	// 7. Without a registered credential, the mTLS backend is skipped entirely.
	devmtls.Clear()
	if got := factory.FromConfig(nil); len(got) != 0 {
		t.Fatalf("expected mTLS backend skipped when unregistered, got %d providers", len(got))
	}
}

// enrollDevice mints a one-time code and enrolls deviceKey's CSR against honey's
// device CA, returning the client cert chain (cert+CA) and the device CA PEM.
func enrollDevice(t *testing.T, honeyBase string, key *ecdsa.PrivateKey, cn string) (chainPEM, caPEM string) {
	t.Helper()

	mintBody, _ := json.Marshal(map[string]string{"cn": cn})
	mintResp := doPost(t, honeyBase+"/api/v1/devices/enroll-code", mintBody)
	var mint struct {
		Code  string `json:"code"`
		CAPem string `json:"ca_pem"`
	}
	if err := json.Unmarshal(mintResp, &mint); err != nil || mint.Code == "" {
		t.Fatalf("mint enroll-code: %v (body %s)", err, mintResp)
	}

	csrDER, err := x509.CreateCertificateRequest(rand.Reader,
		&x509.CertificateRequest{Subject: pkix.Name{CommonName: cn}}, key)
	if err != nil {
		t.Fatalf("create CSR: %v", err)
	}
	csrPEM := pemString("CERTIFICATE REQUEST", csrDER)

	enrollBody, _ := json.Marshal(map[string]string{"code": mint.Code, "csr": csrPEM})
	enrollResp := doPost(t, honeyBase+"/api/v1/devices/enroll", enrollBody)
	var out struct {
		Cert string `json:"cert"`
		CA   string `json:"ca"`
	}
	if err := json.Unmarshal(enrollResp, &out); err != nil || out.Cert == "" {
		t.Fatalf("enroll: %v (body %s)", err, enrollResp)
	}
	return strings.TrimSpace(out.Cert) + "\n" + strings.TrimSpace(out.CA) + "\n", mint.CAPem
}

func doPost(t *testing.T, url string, body []byte) []byte {
	t.Helper()
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s: HTTP %d: %s", url, resp.StatusCode, raw)
	}
	return raw
}

// genServerCert issues a CA + a server cert valid for honey.example / localhost /
// 127.0.0.1 (so the client verifies whichever host testcontainers exposes).
func genServerCert(t *testing.T) (caPEM, certPEM, keyPEM string) {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "gateway-test-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true, IsCA: true,
	}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	caCert, _ := x509.ParseCertificate(caDER)

	// RSA server cert (OpenResty/APISIX is most reliable serving RSA server certs).
	srvKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	srvTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "honey.example"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{"honey.example", "localhost"},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	srvDER, _ := x509.CreateCertificate(rand.Reader, srvTmpl, caCert, &srvKey.PublicKey, caKey)
	// PKCS#8 ("PRIVATE KEY") — OpenResty/APISIX rejects SEC1 "EC PRIVATE KEY".
	srvKeyDER, err := x509.MarshalPKCS8PrivateKey(srvKey)
	if err != nil {
		t.Fatalf("marshal server key: %v", err)
	}

	return pemString("CERTIFICATE", caDER),
		pemString("CERTIFICATE", srvDER),
		pemString("PRIVATE KEY", srvKeyDER)
}

// startMTLSGateway runs an nginx reverse proxy that terminates mTLS in front of
// the in-process honey server: it verifies client certs against clientCAPEM
// (honey's device CA) and proxies to honey, forwarding the client-cert subject as
// X-Honey-User. Returns the https base URL.
//
// nginx (not APISIX) is used here for a deterministic mTLS terminator; the
// production APISIX configuration is documented + shipped under
// examples/mtls/apisix (SSL client.ca + proxy-rewrite $ssl_client_s_dn).
func startMTLSGateway(t *testing.T, honeyPort int, serverCertPEM, serverKeyPEM, clientCAPEM string) string {
	t.Helper()

	nginxConf := fmt.Sprintf(`
events {}
http {
  server {
    listen 9443 ssl;
    server_name _;
    ssl_certificate         /etc/nginx/certs/server.crt;
    ssl_certificate_key     /etc/nginx/certs/server.key;
    ssl_client_certificate  /etc/nginx/certs/client_ca.crt;
    ssl_verify_client on;
    ssl_verify_depth 2;
    location / {
      proxy_pass http://%s:%d;
      proxy_set_header Host $host;
      proxy_set_header X-Honey-User $ssl_client_s_dn;
    }
  }
}
`, testcontainers.HostInternal, honeyPort)

	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil { //nolint:gosec // test fixture
			t.Fatalf("write %s: %v", name, err)
		}
		return p
	}
	confPath := write("nginx.conf", nginxConf)
	certPath := write("server.crt", serverCertPEM)
	keyPath := write("server.key", serverKeyPEM)
	caPath := write("client_ca.crt", clientCAPEM)

	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:           "nginx:1.27-alpine",
		ExposedPorts:    []string{"9443/tcp"},
		HostAccessPorts: []int{honeyPort},
		Files: []testcontainers.ContainerFile{
			{HostFilePath: confPath, ContainerFilePath: "/etc/nginx/nginx.conf", FileMode: 0o644},
			{HostFilePath: certPath, ContainerFilePath: "/etc/nginx/certs/server.crt", FileMode: 0o644},
			{HostFilePath: keyPath, ContainerFilePath: "/etc/nginx/certs/server.key", FileMode: 0o600},
			{HostFilePath: caPath, ContainerFilePath: "/etc/nginx/certs/client_ca.crt", FileMode: 0o644},
		},
		WaitingFor: wait.ForListeningPort("9443/tcp").WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Skipf("start mtls gateway skipped: %v", err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			if rc, e := c.Logs(context.Background()); e == nil {
				b, _ := io.ReadAll(rc)
				t.Logf("gateway logs:\n%s", b)
			}
		}
		_ = c.Terminate(context.Background())
	})

	h, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("gateway host: %v", err)
	}
	p, err := c.MappedPort(ctx, "9443")
	if err != nil {
		t.Fatalf("gateway port: %v", err)
	}
	return fmt.Sprintf("https://%s:%s", h, p.Port())
}

func pemString(typ string, der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}))
}
