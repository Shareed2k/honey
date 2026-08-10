package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/safepath"
)

// kubeSSHLoginPath is the honey web endpoint that signs an SSH public key into a
// short-lived user certificate after verifying the SSO id_token.
const kubeSSHLoginPath = "/api/v1/ssh/login"

var (
	sshLoginAdminURL string
	sshLoginIdentity string
	sshLoginOut      string
)

var sshCmd = &cobra.Command{
	Use:   "ssh",
	Short: "Access hosts through the honey SSH gateway",
}

var sshLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Sign in with SSO and obtain a short-lived SSH certificate for the honey gateway",
	Long: `Runs a browser OIDC sign-in, then exchanges the resulting identity for a
short-lived SSH user certificate signed by honey's SSH CA. The certificate's
principals are attested by honey (mapped from your SSO groups by policy), never
asserted by the client.

The command certifies the key at --identity (an ed25519 key is generated there
if it does not exist) and writes the certificate next to it as
<identity>-cert.pub. Point ssh at the key; OpenSSH loads the matching
-cert.pub automatically:

  honey ssh login --admin-url https://honey.example
  ssh -i ~/.ssh/honey_ed25519 user@host`,
	Args: cobra.NoArgs,
	RunE: runSSHLogin,
}

func init() {
	sshLoginCmd.Flags().StringVar(&sshLoginAdminURL, "admin-url", defaultKubeAdminURL(), "honey web base URL used to sign in (default $HONEY_WEB_URL, else http://localhost:8765)")
	sshLoginCmd.Flags().StringVar(&sshLoginIdentity, "identity", defaultSSHIdentityPath(), "SSH private key to certify; an ed25519 key is generated here if absent")
	sshLoginCmd.Flags().StringVar(&sshLoginOut, "out", "", "certificate output path (default: <identity>-cert.pub)")

	sshCmd.AddCommand(sshLoginCmd)
	rootCmd.AddCommand(sshCmd)
}

func runSSHLogin(cmd *cobra.Command, _ []string) error {
	adminURL := strings.TrimRight(strings.TrimSpace(sshLoginAdminURL), "/")
	if adminURL == "" {
		return fmt.Errorf("--admin-url is required")
	}
	identityPath := strings.TrimSpace(sshLoginIdentity)
	if identityPath == "" {
		return fmt.Errorf("--identity is required")
	}

	signer, err := loadOrCreateSSHIdentity(identityPath)
	if err != nil {
		return err
	}
	pubLine := string(ssh.MarshalAuthorizedKey(signer.PublicKey()))

	idToken, nonce, err := browserAuthCodeFlow(cmd.Context(), adminURL, nil)
	if err != nil {
		return fmt.Errorf("oidc login: %w", err)
	}

	certLine, cn, principals, err := sshOIDCLogin(cmd.Context(), adminURL, idToken, nonce, pubLine)
	if err != nil {
		return err
	}

	outPath := strings.TrimSpace(sshLoginOut)
	if outPath == "" {
		outPath = identityPath + "-cert.pub"
	}
	if err := writeSSHCertFile(outPath, certLine); err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "wrote ssh certificate for %q to %s\n", cn, outPath)
	fmt.Fprintf(out, "  principals: %s\n", strings.Join(principals, ", "))
	fmt.Fprintf(out, "\nNext steps:\n")
	fmt.Fprintf(out, "  ssh -i %s <principal>@<host>\n", identityPath)
	return nil
}

// defaultSSHIdentityPath is the --identity default: ~/.ssh/honey_ed25519, or a
// bare relative name when the home directory cannot be resolved.
func defaultSSHIdentityPath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "honey_ed25519"
	}
	return filepath.Join(home, ".ssh", "honey_ed25519")
}

// loadOrCreateSSHIdentity returns a signer for the SSH private key at path,
// generating a fresh ed25519 key (OpenSSH PEM, mode 0600) when the file does
// not exist. When generating, it also writes the public key to <path>.pub.
func loadOrCreateSSHIdentity(path string) (ssh.Signer, error) {
	if _, statErr := safepath.Stat(path); statErr == nil {
		raw, err := safepath.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read ssh identity %q: %w", path, err)
		}
		signer, err := ssh.ParsePrivateKey(raw)
		if err != nil {
			return nil, fmt.Errorf("parse ssh identity %q: %w", path, err)
		}
		return signer, nil
	} else if !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("stat ssh identity %q: %w", path, statErr)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ssh key: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return nil, fmt.Errorf("marshal ssh key: %w", err)
	}
	if mkErr := safepath.MkdirAll(filepath.Dir(path), 0o700); mkErr != nil {
		return nil, fmt.Errorf("create ssh key directory: %w", mkErr)
	}
	if wErr := safepath.WriteFile(path, pem.EncodeToMemory(block), 0o600); wErr != nil {
		return nil, fmt.Errorf("write ssh key %q: %w", path, wErr)
	}

	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, fmt.Errorf("build ssh signer: %w", err)
	}
	pubLine := ssh.MarshalAuthorizedKey(signer.PublicKey())
	if wErr := safepath.WriteFile(path+".pub", pubLine, 0o644); wErr != nil {
		return nil, fmt.Errorf("write ssh public key %q: %w", path+".pub", wErr)
	}
	return signer, nil
}

// writeSSHCertFile writes the OpenSSH certificate line to path (mode 0600),
// ensuring it ends with a newline and creating the parent directory if needed.
func writeSSHCertFile(path, certLine string) error {
	content := certLine
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if mkErr := safepath.MkdirAll(filepath.Dir(path), 0o700); mkErr != nil {
		return fmt.Errorf("create certificate directory: %w", mkErr)
	}
	if err := safepath.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write certificate %q: %w", path, err)
	}
	return nil
}

// sshLoginResponse mirrors handleSSHLogin's JSON response.
type sshLoginResponse struct {
	CN         string   `json:"cn"`
	Principals []string `json:"principals"`
	Cert       string   `json:"cert"`
}

// sshOIDCLogin exchanges a verified id_token and an SSH public key for a signed
// certificate at the honey web ssh login endpoint.
func sshOIDCLogin(ctx context.Context, adminURL, idToken, nonce, publicKey string) (cert, cn string, principals []string, err error) {
	payload := map[string]string{
		"id_token":   idToken,
		"nonce":      nonce,
		"public_key": publicKey,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", "", nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, adminURL+kubeSSHLoginPath, bytes.NewReader(body))
	if err != nil {
		return "", "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", nil, fmt.Errorf("ssh login: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", "", nil, fmt.Errorf("ssh login: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var lr sshLoginResponse
	if err := json.Unmarshal(raw, &lr); err != nil {
		return "", "", nil, fmt.Errorf("parse response: %w", err)
	}
	if strings.TrimSpace(lr.Cert) == "" || strings.TrimSpace(lr.CN) == "" {
		return "", "", nil, fmt.Errorf("ssh login: response missing cn or cert")
	}
	return lr.Cert, lr.CN, lr.Principals, nil
}
