package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientauthenticationv1 "k8s.io/client-go/pkg/apis/clientauthentication/v1"
	"k8s.io/client-go/tools/clientcmd/api"
)

// credSkew is the renewal lead time applied to a cached certificate's expiry
// when deciding whether it is still usable for exec-credential mode: a
// certificate within credSkew of its NotAfter is treated as stale so kubectl
// is never handed a credential that could expire mid-request.
const credSkew = time.Minute

// execAPIVersion is the client.authentication.k8s.io API version this plugin
// speaks, both as the ExecConfig.APIVersion advertised in the kubeconfig
// (writeExecKubeContext) and as the ExecCredential.APIVersion emitted by
// emitExecCredential; kubectl requires the two to match.
const execAPIVersion = "client.authentication.k8s.io/v1"

// inExecCredentialMode reports whether honey is being invoked as a kubectl
// client-go credential plugin rather than interactively: kubectl sets
// KUBERNETES_EXEC_INFO on every exec-credential invocation. See
// https://kubernetes.io/docs/reference/access-authn-authz/authentication/#client-go-credential-plugins.
func inExecCredentialMode() bool {
	return os.Getenv("KUBERNETES_EXEC_INFO") != ""
}

// execInteractiveAllowed reports whether kubectl has told this invocation
// that it may interact with the user, e.g. open a browser for SSO
// re-authentication. It parses KUBERNETES_EXEC_INFO as a
// client.authentication.k8s.io/v1 ExecCredential and returns its
// spec.interactive value.
//
// Because ExecCredentialSpec.Interactive is a plain bool (not a pointer), its
// zero value is false. That conveniently makes "not allowed" the outcome for
// every case that should be treated conservatively: kubectl explicitly
// setting interactive to false, KUBERNETES_EXEC_INFO being unset, and
// KUBERNETES_EXEC_INFO failing to parse all leave Interactive at its false
// zero value. That is the right default here: it is far better to fail a
// credential request with an error than to pop open a browser that kubectl
// never authorized for this invocation.
func execInteractiveAllowed() bool {
	var ec clientauthenticationv1.ExecCredential
	if err := json.Unmarshal([]byte(os.Getenv("KUBERNETES_EXEC_INFO")), &ec); err != nil {
		return false
	}
	return ec.Spec.Interactive
}

// emitExecCredential marshals c as a client.authentication.k8s.io/v1
// ExecCredential and writes it to w, and only to w. kubectl parses exactly
// one JSON document from this plugin's stdout, so every other line
// (progress, warnings, errors) must go to stderr instead; callers must pass
// cmd.OutOrStdout() here and nothing else. c.CertPEM and c.KeyPEM are placed
// in the response verbatim — never log them.
func emitExecCredential(w io.Writer, c cachedCert) error {
	ec := clientauthenticationv1.ExecCredential{
		TypeMeta: metav1.TypeMeta{
			APIVersion: execAPIVersion,
			Kind:       "ExecCredential",
		},
		Status: &clientauthenticationv1.ExecCredentialStatus{
			ClientCertificateData: string(c.CertPEM),
			ClientKeyData:         string(c.KeyPEM),
			ExpirationTimestamp:   &metav1.Time{Time: c.NotAfter},
		},
	}
	if err := json.NewEncoder(w).Encode(ec); err != nil {
		return fmt.Errorf("encode exec credential: %w", err)
	}
	return nil
}

// runKubeCredential implements honey kube login's kubectl exec-credential
// plugin mode (see inExecCredentialMode): it emits a client.authentication.k8s.io/v1
// ExecCredential for cluster on stdout. A fresh cached certificate is emitted
// with no network access at all. A missing or stale (within credSkew of
// expiry) certificate triggers a browser SSO re-authentication only when
// kubectl has indicated interactive use is allowed (execInteractiveAllowed);
// otherwise this fails closed with an error rather than blocking on a
// browser kubectl did not permit.
func runKubeCredential(cmd *cobra.Command, cluster, adminURL string) error {
	if c, ok := loadCachedCert(cluster); ok && c.isFresh(time.Now(), credSkew) {
		return emitExecCredential(cmd.OutOrStdout(), c)
	}

	if !execInteractiveAllowed() {
		return errors.New("kube: cached certificate expired and kubectl did not allow interactive re-authentication")
	}

	certPEM, keyPEM, _, _, notAfter, err := fetchKubeCertViaSSO(cmd.Context(), adminURL, cluster, nil)
	if err != nil {
		return err
	}

	c := cachedCert{CertPEM: certPEM, KeyPEM: keyPEM, NotAfter: notAfter}
	if err := storeCachedCert(cluster, c); err != nil {
		// The freshly issued certificate is valid; a transient cache-write
		// failure must not force kubectl through another interactive sign-in.
		// Warn to stderr (stdout is reserved for the ExecCredential JSON) and
		// still emit the credential.
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not cache the refreshed certificate: %v\n", err)
	}
	return emitExecCredential(cmd.OutOrStdout(), c)
}

// writeExecKubeContext adds (or replaces) the honey-<cluster> cluster,
// honey-<cn> authInfo, and opts.contextName context in existing, like
// mergeKubeContext, but the authInfo is a kubectl exec credential plugin
// (invoking "honey kube login <cluster> --admin-url <adminURL>" — cluster is
// a positional argument on kubeLoginCmd, not a flag) rather than an embedded
// certificate/key: kubectl re-runs honey on every API call, and honey serves
// a cached certificate or transparently refreshes it via SSO (see
// runKubeCredential). The resulting authInfo carries no secret material at
// all — the certificate/key never touch the kubeconfig file, only honey's
// local cache (storeCachedCert). existing may be nil.
func writeExecKubeContext(existing *api.Config, opts kubeContextOpts, adminURL string) *api.Config {
	cfg, clusterName, authInfoName := kubeContextSkeleton(existing, opts)

	authInfo := api.NewAuthInfo()
	authInfo.Exec = &api.ExecConfig{
		APIVersion:         execAPIVersion,
		Command:            "honey",
		Args:               []string{"kube", "login", opts.cluster, "--admin-url", adminURL},
		InstallHint:        "honey must be on your PATH; install it and re-run kubectl",
		InteractiveMode:    api.IfAvailableExecInteractiveMode,
		ProvideClusterInfo: false,
	}
	cfg.AuthInfos[authInfoName] = authInfo

	finishKubeContext(cfg, opts, clusterName, authInfoName)
	return cfg
}
