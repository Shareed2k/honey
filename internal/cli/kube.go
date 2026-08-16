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
	"time"

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
	kubeLoginStatic     bool
)

// oidcNoBrowser is set by the --no-browser flag: when true, the SSO flow
// should print the sign-in URL instead of attempting to open a browser. It is
// a package var (rather than a local) so the OIDC flow, wherever it ends up
// reading it, can consume it without threading it through every call site.
var oidcNoBrowser bool

// browserAuthCodeFlowFn and kubeOIDCLoginFn are test seams over the real
// browserAuthCodeFlow and kubeOIDCLogin functions: fetchKubeCertViaSSO calls
// through these vars so unit tests can stub out the browser/IdP round trip
// instead of driving a live login.
var (
	browserAuthCodeFlowFn = browserAuthCodeFlow
	kubeOIDCLoginFn       = kubeOIDCLogin
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

By default the SSO path writes a kubeconfig authInfo that invokes "honey kube
login" as a kubectl exec credential plugin, so kubectl transparently
refreshes the certificate via SSO as it nears expiry (honey must stay on
PATH). Pass --static to instead embed the certificate and key directly, as
the --enroll-code path always does (it has no SSO session to refresh from).

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
	kubeLoginCmd.Flags().BoolVar(&kubeLoginStatic, "static", false, "embed the certificate directly instead of the auto-refreshing exec plugin")
	kubeLoginCmd.Flags().BoolVar(&oidcNoBrowser, "no-browser", false, "print the sign-in URL instead of opening a browser")

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
	adminURL := strings.TrimRight(strings.TrimSpace(kubeLoginAdminURL), "/")
	if adminURL == "" {
		return fmt.Errorf("--admin-url is required")
	}

	// kubectl invokes "honey kube login <cluster>" as a client-go credential
	// plugin (see inExecCredentialMode), in which case it wants a single
	// ExecCredential JSON document on stdout and nothing else: --proxy is not
	// needed for that path, so this dispatch happens before the --proxy
	// required check below.
	if inExecCredentialMode() {
		return runKubeCredential(cmd, cluster, adminURL)
	}

	code := strings.TrimSpace(kubeLoginEnrollCode)
	proxy := strings.TrimSpace(kubeLoginProxy)
	if proxy == "" {
		return fmt.Errorf("--proxy is required")
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

	var (
		certPEM  []byte
		keyPEM   []byte
		caPEM    []byte
		cn       string
		notAfter time.Time
		sso      bool
		err      error
	)
	if code != "" {
		// Enroll-code path: an explicit trust source is required, as before.
		if proxyCAPath == "" && !kubeLoginInsecure {
			return fmt.Errorf("one of --proxy-ca or --insecure-skip-tls-verify is required")
		}
		if proxyCAPath == "" {
			fmt.Fprintln(errOut, "warning: --insecure-skip-tls-verify disables verification of the proxy's serving certificate; prefer --proxy-ca")
		}
		var csrPEM []byte
		keyPEM, csrPEM, err = generateKeyAndCSR()
		if err != nil {
			return fmt.Errorf("generate key and csr: %w", err)
		}
		caPEM = flagProxyCA
		certPEM, cn, err = enrollDevice(cmd.Context(), adminURL, code, csrPEM)
		if err != nil {
			return err
		}
	} else {
		// SSO (OIDC) path: browser sign-in, then exchange the id_token + a freshly
		// generated CSR for a signed certificate. The server returns the proxy CA
		// when it knows it.
		sso = true
		var serverCA []byte
		certPEM, keyPEM, serverCA, cn, notAfter, err = fetchKubeCertViaSSO(cmd.Context(), adminURL, cluster, nil)
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

		// Cache the fresh cert so a subsequent kubectl exec-credential
		// invocation (runKubeCredential) can serve it without a network round
		// trip. Caching is best-effort: a failure here must not fail the login
		// that already succeeded, so it is only logged.
		if err := storeCachedCert(cluster, cachedCert{CertPEM: certPEM, KeyPEM: keyPEM, NotAfter: notAfter}); err != nil {
			fmt.Fprintf(errOut, "warning: failed to cache certificate for future refresh: %v\n", err)
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

	opts := kubeContextOpts{
		cluster:               cluster,
		proxy:                 proxy,
		cn:                    cn,
		certPEM:               certPEM,
		keyPEM:                keyPEM,
		caPEM:                 caPEM,
		insecureSkipTLSVerify: kubeLoginInsecure && len(caPEM) == 0,
		contextName:           contextName,
	}

	// The enroll-code path has no SSO session to refresh from, so it is always
	// static regardless of --static. The SSO path defaults to the
	// auto-refreshing exec plugin; --static opts back into the old
	// embedded-certificate behavior.
	useExec := sso && !kubeLoginStatic

	var merged *api.Config
	if useExec {
		merged = writeExecKubeContext(existing, opts, adminURL)
	} else {
		merged = mergeKubeContext(existing, opts)
	}

	if err := writeKubeconfig(kubeconfigPath, merged); err != nil {
		return fmt.Errorf("write kubeconfig %q: %w", kubeconfigPath, err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "wrote kubeconfig context %q to %s\n", contextName, kubeconfigPath)
	fmt.Fprintf(out, "  cluster: honey-%s (https://%s/%s)\n", cluster, proxy, cluster)
	fmt.Fprintf(out, "  user:    honey-%s\n", cn)
	fmt.Fprintf(out, "\nNext steps:\n")
	fmt.Fprintf(out, "  kubectl --context %s get pods\n", contextName)
	if useExec {
		fmt.Fprintf(out, "  kubectl will refresh this certificate automatically by re-running honey; keep honey on your PATH\n")
	}
	return nil
}

// fetchKubeCertViaSSO runs a browser SSO (OIDC) sign-in and exchanges the
// resulting id_token, together with a freshly generated key/CSR pair, for a
// signed mTLS client certificate. It is the single "get a fresh cert" path
// shared by interactive kube login and the kubectl exec credential plugin.
// caPEM is the proxy's serving CA when the server returned one, otherwise
// empty. Never log certPEM or keyPEM.
func fetchKubeCertViaSSO(ctx context.Context, adminURL, cluster string, extraScopes []string) (certPEM, keyPEM, caPEM []byte, cn string, notAfter time.Time, err error) {
	idToken, nonce, err := browserAuthCodeFlowFn(ctx, adminURL, extraScopes)
	if err != nil {
		return nil, nil, nil, "", time.Time{}, fmt.Errorf("oidc login: %w", err)
	}

	keyPEM, csrPEM, err := generateKeyAndCSR()
	if err != nil {
		return nil, nil, nil, "", time.Time{}, fmt.Errorf("generate key and csr: %w", err)
	}

	certPEM, caPEM, cn, _, err = kubeOIDCLoginFn(ctx, adminURL, cluster, idToken, nonce, csrPEM)
	if err != nil {
		return nil, nil, nil, "", time.Time{}, fmt.Errorf("kube oidc login: %w", err)
	}

	notAfter, err = certNotAfter(certPEM)
	if err != nil {
		return nil, nil, nil, "", time.Time{}, fmt.Errorf("parse certificate expiry: %w", err)
	}

	return certPEM, keyPEM, caPEM, cn, notAfter, nil
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
// in existing are left untouched. existing may be nil. The authInfo embeds the
// certificate and key directly (a "static" credential); see writeExecKubeContext
// for the auto-refreshing exec-plugin alternative.
func mergeKubeContext(existing *api.Config, opts kubeContextOpts) *api.Config {
	cfg, clusterName, authInfoName := kubeContextSkeleton(existing, opts)

	authInfo := api.NewAuthInfo()
	authInfo.ClientCertificateData = opts.certPEM
	authInfo.ClientKeyData = opts.keyPEM
	cfg.AuthInfos[authInfoName] = authInfo

	finishKubeContext(cfg, opts, clusterName, authInfoName)
	return cfg
}

// kubeContextSkeleton prepares cfg (creating it, and its Clusters/AuthInfos/
// Contexts maps, when existing is nil or missing them) and writes the
// honey-<cluster> cluster entry shared by mergeKubeContext and
// writeExecKubeContext: same server URL, same CA/insecure handling. It returns
// cfg along with the derived cluster and authInfo map keys so callers can fill
// in their own authInfo.
func kubeContextSkeleton(existing *api.Config, opts kubeContextOpts) (cfg *api.Config, clusterName, authInfoName string) {
	cfg = existing
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

	clusterName = "honey-" + opts.cluster
	authInfoName = "honey-" + opts.cn

	cluster := api.NewCluster()
	cluster.Server = "https://" + opts.proxy + "/" + opts.cluster
	if opts.insecureSkipTLSVerify {
		cluster.InsecureSkipTLSVerify = true
	} else {
		cluster.CertificateAuthorityData = opts.caPEM
	}
	cfg.Clusters[clusterName] = cluster

	return cfg, clusterName, authInfoName
}

// finishKubeContext writes opts.contextName pointing at clusterName/
// authInfoName into cfg and makes it the current context. Shared tail of
// mergeKubeContext and writeExecKubeContext, once each has populated its own
// authInfo.
func finishKubeContext(cfg *api.Config, opts kubeContextOpts, clusterName, authInfoName string) {
	kubeContext := api.NewContext()
	kubeContext.Cluster = clusterName
	kubeContext.AuthInfo = authInfoName
	cfg.Contexts[opts.contextName] = kubeContext

	cfg.CurrentContext = opts.contextName
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
