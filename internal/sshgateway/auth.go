package sshgateway

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Extension keys carried in ssh.Permissions from the auth callback to the
// session handler. They are honey-internal and never sent to the client.
const (
	extActor     = "honey-actor"
	extPrincipal = "honey-principal"
	extUserAttr  = "honey-user-attr"
)

// certAuthConfig carries the trust store and identity-mapping settings for
// user-certificate authentication.
type certAuthConfig struct {
	trustedCAs []ssh.PublicKey
	// userAttr labels which identity attribute the actor was taken from; it is
	// recorded for audit and does not weaken the certificate principal binding.
	userAttr string
	// certAttr selects the certificate field used as the honey actor:
	// "principal" (the validated ssh login principal) or "key_id".
	certAttr string
}

// buildServerConfig builds the *ssh.ServerConfig for the gateway.
//
// When disableAuth is true the server accepts any client (NoClientAuth) — dev
// only. Otherwise a user SSH certificate signed by one of ca.trustedCAs is
// required: ssh.CertChecker verifies the signature, cert type, validity window,
// and that the ssh login user is a permitted principal; the honey actor is then
// derived per ca.certAttr and recorded in ssh.Permissions.Extensions for the
// session handler. Deny-by-default: without a trusted CA (and without
// disableAuth) construction fails.
func buildServerConfig(hostKey ssh.Signer, disableAuth bool, ca certAuthConfig) (*ssh.ServerConfig, error) {
	if hostKey == nil {
		return nil, fmt.Errorf("host key is required")
	}
	cfg := &ssh.ServerConfig{}
	if disableAuth {
		cfg.NoClientAuth = true
	} else {
		if len(ca.trustedCAs) == 0 {
			return nil, fmt.Errorf("no trusted CA configured (set trusted_ca or enable --no-auth)")
		}
		cfg.PublicKeyCallback = certPublicKeyCallback(ca)
	}
	cfg.AddHostKey(hostKey)
	return cfg, nil
}

// certPublicKeyCallback returns a PublicKeyCallback that validates a user
// certificate against the trusted CAs (via ssh.CertChecker) and maps it to a
// honey actor stored in the returned Permissions.
func certPublicKeyCallback(ca certAuthConfig) func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
	trusted := make(map[string]struct{}, len(ca.trustedCAs))
	for _, k := range ca.trustedCAs {
		if k != nil {
			trusted[string(k.Marshal())] = struct{}{}
		}
	}
	checker := &ssh.CertChecker{
		IsUserAuthority: func(auth ssh.PublicKey) bool {
			_, ok := trusted[string(auth.Marshal())]
			return ok
		},
	}
	certAttr := normalizeAttr(ca.certAttr)
	userAttr := normalizeAttr(ca.userAttr)

	return func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		cert, ok := key.(*ssh.Certificate)
		if !ok {
			return nil, fmt.Errorf("ssh certificate required (plain public keys are not accepted)")
		}
		// CertChecker enforces signature, UserCert type, validity window, and
		// that conn.User() is a permitted principal.
		perms, err := checker.Authenticate(conn, key)
		if err != nil {
			return nil, err
		}
		actor, err := actorFromCert(conn, cert, certAttr)
		if err != nil {
			return nil, err
		}

		out := &ssh.Permissions{}
		if perms != nil {
			out.CriticalOptions = perms.CriticalOptions
			out.Extensions = cloneStringMap(perms.Extensions)
		}
		if out.Extensions == nil {
			out.Extensions = map[string]string{}
		}
		out.Extensions[extActor] = actor
		out.Extensions[extPrincipal] = conn.User()
		out.Extensions[extUserAttr] = userAttr
		return out, nil
	}
}

// actorFromCert derives the honey actor identity from the certificate per
// certAttr.
func actorFromCert(conn ssh.ConnMetadata, cert *ssh.Certificate, certAttr string) (string, error) {
	switch certAttr {
	case "key_id", "keyid":
		id := strings.TrimSpace(cert.KeyId)
		if id == "" {
			return "", fmt.Errorf("certificate key_id is empty (required by cert_attr=key_id)")
		}
		return id, nil
	default: // "principal"
		if u := strings.TrimSpace(conn.User()); u != "" {
			return u, nil
		}
		return "", fmt.Errorf("empty ssh login principal")
	}
}

func normalizeAttr(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "principal"
	}
	return s
}

func cloneStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
