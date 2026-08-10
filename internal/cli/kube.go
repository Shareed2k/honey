package cli

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"

	"github.com/shareed2k/honey/internal/safepath"
)

const (
	kubeDeviceEnrollPath = "/api/v1/devices/enroll"
	kubeOIDCLoginPath    = "/api/v1/kube/login"
)

var (
	kubeLoginEnrollCode string
	kubeLoginProxy      string
	kubeLoginAdminURL   string
	kubeLoginProxyCA    string
	kubeLoginInsecure   bool
	kubeLoginKubeconfig string
	kubeLoginContext    string
)

var kubeCmd = &cobra.Command{
	Use:   "kube",
	Short: "Manage kubectl access through the honey Kubernetes access proxy",
}

var kubeLoginCmd = &cobra.Command{
	Use:   "login <cluster>",
	Short: "Enroll a device certificate and write a kubeconfig context for the honey k8s proxy",
	Long: `Obtains a short-lived client certificate signed by honey's device CA and
writes a kubeconfig cluster/user/context that points kubectl at the honey
Kubernetes access proxy for the given cluster.

Two identity sources are supported:
  - With --enroll-code: redeems a one-time device enrollment code minted by an
    operator via honey device enroll-code (the code fixes the certificate
    identity).
  - Without --enroll-code: runs a browser SSO (OIDC) sign-in; honey maps the
    verified identity to a Kubernetes user and groups via policy, and returns
    the proxy's serving CA so --proxy-ca is not required.

Example:
  honey kube login prod --enroll-code abc123 --proxy proxy.example:6443 \
    --proxy-ca proxy-ca.pem
  honey kube login prod --proxy proxy.example:6443
  kubectl --context honey-prod get pods`,
	Args: cobra.ExactArgs(1),
	RunE: runKubeLogin,
}

func init() {
	kubeLoginCmd.Flags().StringVar(&kubeLoginEnrollCode, "enroll-code", "", "One-time enrollment code from honey device enroll-code (fixes the certificate identity); when empty, a browser SSO sign-in is used instead")
	kubeLoginCmd.Flags().StringVar(&kubeLoginProxy, "proxy", "", "honey k8s-proxy address kubectl connects to, host:port (required)")
	kubeLoginCmd.Flags().StringVar(&kubeLoginAdminURL, "admin-url", defaultKubeAdminURL(), "honey web base URL used to redeem the enrollment code (default $HONEY_WEB_URL, else http://localhost:8765)")
	kubeLoginCmd.Flags().StringVar(&kubeLoginProxyCA, "proxy-ca", "", "PEM file with the CA that signed the proxy's serving certificate")
	kubeLoginCmd.Flags().BoolVar(&kubeLoginInsecure, "insecure-skip-tls-verify", false, "Skip verification of the proxy's serving certificate instead of pinning --proxy-ca (insecure)")
	kubeLoginCmd.Flags().StringVar(&kubeLoginKubeconfig, "kubeconfig", "", "kubeconfig file to update (default: $KUBECONFIG first entry, else ~/.kube/config)")
	kubeLoginCmd.Flags().StringVar(&kubeLoginContext, "context", "", "kubectl context name to create (default: honey-<cluster>)")

	kubeCmd.AddCommand(kubeLoginCmd)
	rootCmd.AddCommand(kubeCmd)
}

// defaultKubeAdminURL is the --admin-url flag default: $HONEY_WEB_URL when
// set, otherwise the honey web default listen address.
func defaultKubeAdminURL() string {
	if v := strings.TrimSpace(os.Getenv("HONEY_WEB_URL")); v != "" {
		return v
	}
	return "http://localhost:8765"
}

func runKubeLogin(cmd *cobra.Command, args []string) error {
	cluster := strings.TrimSpace(args[0])
	if cluster == "" {
		return fmt.Errorf("cluster name is required")
	}
	code := strings.TrimSpace(kubeLoginEnrollCode)
	proxy := strings.TrimSpace(kubeLoginProxy)
	if proxy == "" {
		return fmt.Errorf("--proxy is required")
	}
	adminURL := strings.TrimRight(strings.TrimSpace(kubeLoginAdminURL), "/")
	if adminURL == "" {
		return fmt.Errorf("--admin-url is required")
	}

	proxyCAPath := strings.TrimSpace(kubeLoginProxyCA)
	if proxyCAPath != "" && kubeLoginInsecure {
		return fmt.Errorf("--proxy-ca and --insecure-skip-tls-verify are mutually exclusive")
	}

	errOut := cmd.ErrOrStderr()
	var flagProxyCA []byte
	if proxyCAPath != "" {
		var err error
		flagProxyCA, err = safepath.ReadFile(proxyCAPath)
		if err != nil {
			return fmt.Errorf("read proxy CA %q: %w", proxyCAPath, err)
		}
	}

	keyPEM, csrPEM, err := generateKeyAndCSR()
	if err != nil {
		return fmt.Errorf("generate key and csr: %w", err)
	}

	var (
		certPEM []byte
		caPEM   []byte
		cn      string
	)
	if code != "" {
		// Enroll-code path: an explicit trust source is required, as before.
		if proxyCAPath == "" && !kubeLoginInsecure {
			return fmt.Errorf("one of --proxy-ca or --insecure-skip-tls-verify is required")
		}
		if proxyCAPath == "" {
			fmt.Fprintln(errOut, "warning: --insecure-skip-tls-verify disables verification of the proxy's serving certificate; prefer --proxy-ca")
		}
		caPEM = flagProxyCA
		certPEM, cn, err = enrollDevice(cmd.Context(), adminURL, code, csrPEM)
		if err != nil {
			return err
		}
	} else {
		// SSO (OIDC) path: browser sign-in, then exchange the id_token + CSR for a
		// signed certificate. The server returns the proxy CA when it knows it.
		idToken, nonce, ferr := browserAuthCodeFlow(cmd.Context(), adminURL, nil)
		if ferr != nil {
			return fmt.Errorf("oidc login: %w", ferr)
		}
		var serverCA []byte
		certPEM, serverCA, cn, _, err = kubeOIDCLogin(cmd.Context(), adminURL, cluster, idToken, nonce, csrPEM)
		if err != nil {
			return err
		}
		switch {
		case len(serverCA) > 0:
			caPEM = serverCA
		case len(flagProxyCA) > 0:
			caPEM = flagProxyCA
		case kubeLoginInsecure:
			fmt.Fprintln(errOut, "warning: --insecure-skip-tls-verify disables verification of the proxy's serving certificate; prefer --proxy-ca")
		default:
			return fmt.Errorf("login response did not include a proxy CA; provide --proxy-ca or --insecure-skip-tls-verify")
		}
	}

	kubeconfigPath := strings.TrimSpace(kubeLoginKubeconfig)
	if kubeconfigPath == "" {
		kubeconfigPath = defaultKubeconfigPath()
	}

	contextName := strings.TrimSpace(kubeLoginContext)
	if contextName == "" {
		contextName = "honey-" + cluster
	}

	existing, err := loadOrNewKubeconfig(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("load kubeconfig %q: %w", kubeconfigPath, err)
	}

	merged := mergeKubeContext(existing, kubeContextOpts{
		cluster:               cluster,
		proxy:                 proxy,
		cn:                    cn,
		certPEM:               certPEM,
		keyPEM:                keyPEM,
		caPEM:                 caPEM,
		insecureSkipTLSVerify: kubeLoginInsecure && len(caPEM) == 0,
		contextName:           contextName,
	})

	if err := writeKubeconfig(kubeconfigPath, merged); err != nil {
		return fmt.Errorf("write kubeconfig %q: %w", kubeconfigPath, err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "wrote kubeconfig context %q to %s\n", contextName, kubeconfigPath)
	fmt.Fprintf(out, "  cluster: honey-%s (https://%s/%s)\n", cluster, proxy, cluster)
	fmt.Fprintf(out, "  user:    honey-%s\n", cn)
	fmt.Fprintf(out, "\nNext steps:\n")
	fmt.Fprintf(out, "  kubectl --context %s get pods\n", contextName)
	return nil
}

// generateKeyAndCSR creates an EC P-256 key and a PEM-encoded certificate
// signing request for it. The CSR's Subject CN is a placeholder: the server
// overrides it with the enrollment code's stored CN when it signs the cert.
func generateKeyAndCSR() (keyPEM, csrPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate key: %w", err)
	}
	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal key: %w", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	template := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "honey-kube"},
	}
	csrBytes, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		return nil, nil, fmt.Errorf("create csr: %w", err)
	}
	csrPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrBytes})
	return keyPEM, csrPEM, nil
}

// deviceEnrollResponse mirrors handleDeviceEnroll's JSON response.
type deviceEnrollResponse struct {
	CN   string `json:"cn"`
	Cert string `json:"cert"`
	CA   string `json:"ca"`
}

// enrollDevice redeems the one-time code for a signed client certificate. The
// code alone authenticates the request; no honey session token is sent.
func enrollDevice(ctx context.Context, adminURL, code string, csrPEM []byte) (certPEM []byte, cn string, err error) {
	payload := map[string]string{
		"code": code,
		"csr":  string(csrPEM),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, adminURL+kubeDeviceEnrollPath, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("enroll device: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("enroll device: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var er deviceEnrollResponse
	if err := json.Unmarshal(raw, &er); err != nil {
		return nil, "", fmt.Errorf("parse response: %w", err)
	}
	if strings.TrimSpace(er.Cert) == "" || strings.TrimSpace(er.CN) == "" {
		return nil, "", fmt.Errorf("enroll device: response missing cn or cert")
	}
	return []byte(er.Cert), er.CN, nil
}

// kubeLoginResponse mirrors handleKubeLogin's JSON response. proxy_ca may be
// absent when the server does not know its own serving CA.
type kubeLoginResponse struct {
	CN      string   `json:"cn"`
	Groups  []string `json:"groups"`
	Cert    string   `json:"cert"`
	ProxyCA string   `json:"proxy_ca"`
}

// kubeOIDCLogin exchanges a verified id_token, its nonce, and a CSR for a signed
// mTLS client certificate at the honey web kube login endpoint. The id_token
// authenticates the request; no honey session token is sent. caPEM is the
// proxy's serving CA, empty when the server did not include one.
func kubeOIDCLogin(ctx context.Context, adminURL, cluster, idToken, nonce string, csrPEM []byte) (certPEM, caPEM []byte, cn string, groups []string, err error) {
	payload := map[string]string{
		"id_token": idToken,
		"nonce":    nonce,
		"csr":      string(csrPEM),
		"cluster":  cluster,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, "", nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, adminURL+kubeOIDCLoginPath, bytes.NewReader(body))
	if err != nil {
		return nil, nil, "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, "", nil, fmt.Errorf("kube login: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, nil, "", nil, fmt.Errorf("kube login: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var lr kubeLoginResponse
	if err := json.Unmarshal(raw, &lr); err != nil {
		return nil, nil, "", nil, fmt.Errorf("parse response: %w", err)
	}
	if strings.TrimSpace(lr.Cert) == "" || strings.TrimSpace(lr.CN) == "" {
		return nil, nil, "", nil, fmt.Errorf("kube login: response missing cn or cert")
	}
	return []byte(lr.Cert), []byte(lr.ProxyCA), lr.CN, lr.Groups, nil
}

// kubeContextOpts configures the cluster/authInfo/context triple that
// mergeKubeContext writes into a kubeconfig.
type kubeContextOpts struct {
	cluster               string // target cluster name (the proxy path segment)
	proxy                 string // proxy address, host:port
	cn                    string // device certificate CN, names the authInfo
	certPEM               []byte
	keyPEM                []byte
	caPEM                 []byte // proxy serving CA; ignored when insecureSkipTLSVerify
	insecureSkipTLSVerify bool
	contextName           string
}

// mergeKubeContext adds (or replaces) the honey-<cluster> cluster, honey-<cn>
// authInfo, and opts.contextName context in existing, and points
// CurrentContext at the new context. All other clusters/users/contexts already
// in existing are left untouched. existing may be nil.
func mergeKubeContext(existing *api.Config, opts kubeContextOpts) *api.Config {
	cfg := existing
	if cfg == nil {
		cfg = api.NewConfig()
	}
	if cfg.Clusters == nil {
		cfg.Clusters = map[string]*api.Cluster{}
	}
	if cfg.AuthInfos == nil {
		cfg.AuthInfos = map[string]*api.AuthInfo{}
	}
	if cfg.Contexts == nil {
		cfg.Contexts = map[string]*api.Context{}
	}

	clusterName := "honey-" + opts.cluster
	authInfoName := "honey-" + opts.cn

	cluster := api.NewCluster()
	cluster.Server = "https://" + opts.proxy + "/" + opts.cluster
	if opts.insecureSkipTLSVerify {
		cluster.InsecureSkipTLSVerify = true
	} else {
		cluster.CertificateAuthorityData = opts.caPEM
	}
	cfg.Clusters[clusterName] = cluster

	authInfo := api.NewAuthInfo()
	authInfo.ClientCertificateData = opts.certPEM
	authInfo.ClientKeyData = opts.keyPEM
	cfg.AuthInfos[authInfoName] = authInfo

	kubeContext := api.NewContext()
	kubeContext.Cluster = clusterName
	kubeContext.AuthInfo = authInfoName
	cfg.Contexts[opts.contextName] = kubeContext

	cfg.CurrentContext = opts.contextName
	return cfg
}

// defaultKubeconfigPath resolves the target kubeconfig file when --kubeconfig
// is not given: the first entry of $KUBECONFIG, otherwise ~/.kube/config.
func defaultKubeconfigPath() string {
	if v := strings.TrimSpace(os.Getenv("KUBECONFIG")); v != "" {
		if list := filepath.SplitList(v); len(list) > 0 && strings.TrimSpace(list[0]) != "" {
			return list[0]
		}
	}
	return clientcmd.RecommendedHomeFile
}

// loadOrNewKubeconfig loads the kubeconfig at path, tolerating a missing file
// by returning a fresh empty config.
func loadOrNewKubeconfig(path string) (*api.Config, error) {
	cfg, err := clientcmd.LoadFromFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return api.NewConfig(), nil
		}
		return nil, err
	}
	return cfg, nil
}

// writeKubeconfig writes cfg to path, creating the parent directory (0700)
// first if the file does not exist yet.
func writeKubeconfig(path string, cfg *api.Config) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if mkErr := safepath.MkdirAll(filepath.Dir(path), 0o700); mkErr != nil {
			return fmt.Errorf("create kubeconfig directory: %w", mkErr)
		}
	}
	return clientcmd.WriteToFile(*cfg, path)
}
