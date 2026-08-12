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
)

// credSkew is the renewal lead time applied to a cached certificate's expiry
// when deciding whether it is still usable for exec-credential mode: a
// certificate within credSkew of its NotAfter is treated as stale so kubectl
// is never handed a credential that could expire mid-request.
const credSkew = time.Minute

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
			APIVersion: "client.authentication.k8s.io/v1",
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
		return fmt.Errorf("store refreshed certificate: %w", err)
	}
	return emitExecCredential(cmd.OutOrStdout(), c)
}
