package cli

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s.io/client-go/tools/clientcmd/api"
)

func TestMergeKubeContext(t *testing.T) {
	baseOpts := kubeContextOpts{
		cluster:     "prod",
		proxy:       "proxy.example:6443",
		cn:          "alice",
		certPEM:     []byte("cert-pem"),
		keyPEM:      []byte("key-pem"),
		caPEM:       []byte("ca-pem"),
		contextName: "honey-prod",
	}

	t.Run("empty config", func(t *testing.T) {
		got := mergeKubeContext(nil, baseOpts)

		cluster, ok := got.Clusters["honey-prod"]
		if !ok {
			t.Fatalf("expected cluster honey-prod, got %v", got.Clusters)
		}
		if want := "https://proxy.example:6443/prod"; cluster.Server != want {
			t.Errorf("cluster.Server = %q, want %q", cluster.Server, want)
		}
		if string(cluster.CertificateAuthorityData) != "ca-pem" {
			t.Errorf("cluster.CertificateAuthorityData = %q, want %q", cluster.CertificateAuthorityData, "ca-pem")
		}
		if cluster.InsecureSkipTLSVerify {
			t.Errorf("cluster.InsecureSkipTLSVerify = true, want false")
		}

		authInfo, ok := got.AuthInfos["honey-alice"]
		if !ok {
			t.Fatalf("expected authInfo honey-alice, got %v", got.AuthInfos)
		}
		if string(authInfo.ClientCertificateData) != "cert-pem" {
			t.Errorf("authInfo.ClientCertificateData = %q, want %q", authInfo.ClientCertificateData, "cert-pem")
		}
		if string(authInfo.ClientKeyData) != "key-pem" {
			t.Errorf("authInfo.ClientKeyData = %q, want %q", authInfo.ClientKeyData, "key-pem")
		}

		ctx, ok := got.Contexts["honey-prod"]
		if !ok {
			t.Fatalf("expected context honey-prod, got %v", got.Contexts)
		}
		if ctx.Cluster != "honey-prod" || ctx.AuthInfo != "honey-alice" {
			t.Errorf("context = %+v, want cluster=honey-prod authInfo=honey-alice", ctx)
		}
		if got.CurrentContext != "honey-prod" {
			t.Errorf("CurrentContext = %q, want honey-prod", got.CurrentContext)
		}
	})

	t.Run("insecure skip tls verify sets flag not CA data", func(t *testing.T) {
		opts := baseOpts
		opts.insecureSkipTLSVerify = true
		opts.caPEM = nil
		got := mergeKubeContext(nil, opts)

		cluster := got.Clusters["honey-prod"]
		if !cluster.InsecureSkipTLSVerify {
			t.Errorf("cluster.InsecureSkipTLSVerify = false, want true")
		}
		if len(cluster.CertificateAuthorityData) != 0 {
			t.Errorf("cluster.CertificateAuthorityData = %q, want empty", cluster.CertificateAuthorityData)
		}
	})

	t.Run("preserves unrelated existing entries", func(t *testing.T) {
		existing := api.NewConfig()
		existing.Clusters["other-cluster"] = &api.Cluster{Server: "https://other.example"}
		existing.AuthInfos["other-user"] = &api.AuthInfo{Token: "tok"}
		existing.Contexts["other-context"] = &api.Context{Cluster: "other-cluster", AuthInfo: "other-user"}
		existing.CurrentContext = "other-context"

		got := mergeKubeContext(existing, baseOpts)

		if _, ok := got.Clusters["other-cluster"]; !ok {
			t.Errorf("expected other-cluster to be preserved, got %v", got.Clusters)
		}
		if _, ok := got.AuthInfos["other-user"]; !ok {
			t.Errorf("expected other-user to be preserved, got %v", got.AuthInfos)
		}
		if _, ok := got.Contexts["other-context"]; !ok {
			t.Errorf("expected other-context to be preserved, got %v", got.Contexts)
		}
		if _, ok := got.Clusters["honey-prod"]; !ok {
			t.Errorf("expected honey-prod to be added, got %v", got.Clusters)
		}
		if got.CurrentContext != "honey-prod" {
			t.Errorf("CurrentContext = %q, want honey-prod (new login takes over)", got.CurrentContext)
		}
	})

	t.Run("rerunning replaces instead of duplicating", func(t *testing.T) {
		first := mergeKubeContext(nil, baseOpts)

		updated := baseOpts
		updated.certPEM = []byte("new-cert-pem")
		updated.keyPEM = []byte("new-key-pem")
		second := mergeKubeContext(first, updated)

		if len(second.Clusters) != 1 {
			t.Errorf("Clusters = %v, want exactly 1 entry (no duplication)", second.Clusters)
		}
		if len(second.AuthInfos) != 1 {
			t.Errorf("AuthInfos = %v, want exactly 1 entry (no duplication)", second.AuthInfos)
		}
		if len(second.Contexts) != 1 {
			t.Errorf("Contexts = %v, want exactly 1 entry (no duplication)", second.Contexts)
		}
		authInfo := second.AuthInfos["honey-alice"]
		if string(authInfo.ClientCertificateData) != "new-cert-pem" {
			t.Errorf("authInfo.ClientCertificateData = %q, want %q (replaced)", authInfo.ClientCertificateData, "new-cert-pem")
		}
	})
}

func TestGenerateKeyAndCSR(t *testing.T) {
	keyPEM, csrPEM, err := generateKeyAndCSR()
	if err != nil {
		t.Fatalf("generateKeyAndCSR: %v", err)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		t.Fatalf("key PEM did not decode: %s", keyPEM)
	}
	if keyBlock.Type != "EC PRIVATE KEY" {
		t.Errorf("key PEM type = %q, want EC PRIVATE KEY", keyBlock.Type)
	}
	if _, err := x509.ParseECPrivateKey(keyBlock.Bytes); err != nil {
		t.Errorf("parse EC private key: %v", err)
	}

	csrBlock, _ := pem.Decode(csrPEM)
	if csrBlock == nil {
		t.Fatalf("csr PEM did not decode: %s", csrPEM)
	}
	if csrBlock.Type != "CERTIFICATE REQUEST" {
		t.Errorf("csr PEM type = %q, want CERTIFICATE REQUEST", csrBlock.Type)
	}
	csr, err := x509.ParseCertificateRequest(csrBlock.Bytes)
	if err != nil {
		t.Fatalf("parse certificate request: %v", err)
	}
	if err := csr.CheckSignature(); err != nil {
		t.Errorf("csr signature invalid: %v", err)
	}
}

func TestEnrollDevice(t *testing.T) {
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != kubeDeviceEnrollPath {
			t.Errorf("path = %s, want %s", r.URL.Path, kubeDeviceEnrollPath)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"cn":   "alice",
			"cert": "-----BEGIN CERTIFICATE-----\nfake\n-----END CERTIFICATE-----\n",
			"ca":   "-----BEGIN CERTIFICATE-----\nfake-ca\n-----END CERTIFICATE-----\n",
		})
	}))
	defer srv.Close()

	_, csrPEM, err := generateKeyAndCSR()
	if err != nil {
		t.Fatalf("generateKeyAndCSR: %v", err)
	}

	certPEM, cn, err := enrollDevice(context.Background(), srv.URL, "the-code", csrPEM)
	if err != nil {
		t.Fatalf("enrollDevice: %v", err)
	}

	if gotBody["code"] != "the-code" {
		t.Errorf("request code = %q, want %q", gotBody["code"], "the-code")
	}
	if block, _ := pem.Decode([]byte(gotBody["csr"])); block == nil || block.Type != "CERTIFICATE REQUEST" {
		t.Errorf("request csr did not carry a PEM certificate request: %q", gotBody["csr"])
	}

	if cn != "alice" {
		t.Errorf("cn = %q, want alice", cn)
	}
	if string(certPEM) != "-----BEGIN CERTIFICATE-----\nfake\n-----END CERTIFICATE-----\n" {
		t.Errorf("certPEM = %q, unexpected", certPEM)
	}

	merged := mergeKubeContext(nil, kubeContextOpts{
		cluster:     "prod",
		proxy:       "proxy.example:6443",
		cn:          cn,
		certPEM:     certPEM,
		keyPEM:      []byte("key-pem"),
		caPEM:       []byte("ca-pem"),
		contextName: "honey-prod",
	})
	if string(merged.AuthInfos["honey-alice"].ClientCertificateData) != string(certPEM) {
		t.Errorf("kubeconfig cert = %q, want %q", merged.AuthInfos["honey-alice"].ClientCertificateData, certPEM)
	}
}

func TestEnrollDevice_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"invalid or expired enrollment code"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, _, err := enrollDevice(context.Background(), srv.URL, "bad-code", []byte("csr"))
	if err == nil {
		t.Fatal("expected error for non-200 response, got nil")
	}
}
