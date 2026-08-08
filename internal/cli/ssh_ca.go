package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/safepath"
	"github.com/shareed2k/honey/internal/sshca"
)

var (
	sshCADir string

	sshCASignPubkey     string
	sshCASignPrincipals []string
	sshCASignKeyID      string
	sshCASignTTL        time.Duration
	sshCASignOut        string
)

var sshCACmd = &cobra.Command{
	Use:   "ssh-ca",
	Short: "Manage the SSH certificate authority used by the SSH gateway",
	Long: `Manage the built-in SSH certificate authority. The CA mints short-lived user
certificates so operators/CI authenticate to the SSH gateway (honey ssh-server)
without distributing per-user keys to targets. The CA public key is what the
gateway trusts (ssh_gateway.trusted_ca / --trusted-ca); the gateway also
auto-trusts this CA from the state dir once it has been created.`,
}

var sshCAInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create (or load) the SSH CA and print its public key",
	Long: `Creates the SSH CA keypair under the state dir if it does not exist, then prints
the CA public key as an authorized_keys line on stdout. Add it to
ssh_gateway.trusted_ca (or pass --trusted-ca <file>); honey ssh-server also
auto-trusts this CA from the state dir.`,
	Args: cobra.NoArgs,
	RunE: runSSHCAInit,
}

var sshCAPrintCmd = &cobra.Command{
	Use:   "print-ca",
	Short: "Print the SSH CA public key (authorized_keys line)",
	Args:  cobra.NoArgs,
	RunE:  runSSHCAPrint,
}

var sshCASignCmd = &cobra.Command{
	Use:   "sign",
	Short: "Sign a user SSH public key into a short-lived certificate",
	Long: `Signs a user's SSH public key into a short-lived OpenSSH user certificate valid
for the given principals. The user then connects with both the private key and
the issued certificate:

  honey ssh-ca sign --pubkey alice.pub --principal alice --ttl 1h
  ssh -i alice -i alice-cert.pub alice@gateway -p 12222 <resource>`,
	Args: cobra.NoArgs,
	RunE: runSSHCASign,
}

func init() {
	sshCACmd.PersistentFlags().StringVar(&sshCADir, "dir", "", "Directory holding the SSH CA key (default: state dir)")

	sshCASignCmd.Flags().StringVar(&sshCASignPubkey, "pubkey", "", "Path to the user's SSH public key to sign (required)")
	sshCASignCmd.Flags().StringArrayVar(&sshCASignPrincipals, "principal", nil, "Principal the certificate is valid for (repeatable; at least one required)")
	sshCASignCmd.Flags().StringVar(&sshCASignKeyID, "key-id", "", "Certificate key ID (default: first principal)")
	sshCASignCmd.Flags().DurationVar(&sshCASignTTL, "ttl", time.Hour, "Certificate validity duration")
	sshCASignCmd.Flags().StringVar(&sshCASignOut, "out", "", "Output path for the certificate ('-' for stdout; default: <pubkey>-cert.pub)")

	sshCACmd.AddCommand(sshCAInitCmd, sshCAPrintCmd, sshCASignCmd)
	rootCmd.AddCommand(sshCACmd)
}

// resolveSSHCADir returns the CA directory: the --dir flag when set, otherwise
// the resolved honey state dir (same location the gateway host key uses).
func resolveSSHCADir() (string, error) {
	if dir := strings.TrimSpace(sshCADir); dir != "" {
		return dir, nil
	}
	dir, err := config.ResolveStateDir()
	if err != nil {
		return "", fmt.Errorf("resolve state dir: %w", err)
	}
	return dir, nil
}

func runSSHCAInit(cmd *cobra.Command, _ []string) error {
	dir, err := resolveSSHCADir()
	if err != nil {
		return err
	}
	ca, err := sshca.LoadOrCreateCA(dir)
	if err != nil {
		return err
	}
	// The authorized_keys line goes to stdout (pipe-friendly); hints to stderr.
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s", ca.AuthorizedKey()); err != nil {
		return err
	}
	errOut := cmd.ErrOrStderr()
	fmt.Fprintf(errOut, "\nSSH CA ready in %s\n", dir)
	fmt.Fprintf(errOut, "Add the public key above to ssh_gateway.trusted_ca (or pass --trusted-ca <file>).\n")
	fmt.Fprintf(errOut, "honey ssh-server also auto-trusts this CA from the state dir.\n")
	return nil
}

func runSSHCAPrint(cmd *cobra.Command, _ []string) error {
	dir, err := resolveSSHCADir()
	if err != nil {
		return err
	}
	pub, ok, err := sshca.LoadCAPublicKey(dir)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no SSH CA found in %s: run `honey ssh-ca init` first", dir)
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s", ssh.MarshalAuthorizedKey(pub))
	return err
}

func runSSHCASign(cmd *cobra.Command, _ []string) error {
	pubPath := strings.TrimSpace(sshCASignPubkey)
	if pubPath == "" {
		return fmt.Errorf("--pubkey is required")
	}
	principals := trimmedNonEmpty(sshCASignPrincipals)
	if len(principals) == 0 {
		return fmt.Errorf("--principal is required (at least one)")
	}

	dir, err := resolveSSHCADir()
	if err != nil {
		return err
	}
	ca, err := sshca.LoadOrCreateCA(dir)
	if err != nil {
		return err
	}

	raw, err := safepath.ReadFile(pubPath)
	if err != nil {
		return fmt.Errorf("read pubkey %q: %w", pubPath, err)
	}
	userKey, _, _, _, err := ssh.ParseAuthorizedKey(raw)
	if err != nil {
		return fmt.Errorf("parse pubkey %q: %w", pubPath, err)
	}

	keyID := strings.TrimSpace(sshCASignKeyID)
	if keyID == "" {
		keyID = principals[0]
	}

	cert, err := ca.Sign(sshca.SignRequest{
		PublicKey:  userKey,
		KeyID:      keyID,
		Principals: principals,
		TTL:        sshCASignTTL,
	})
	if err != nil {
		return err
	}
	certLine := ssh.MarshalAuthorizedKey(cert)

	out := strings.TrimSpace(sshCASignOut)
	if out == "-" {
		_, err = cmd.OutOrStdout().Write(certLine)
		return err
	}
	if out == "" {
		base := strings.TrimSuffix(filepath.Base(pubPath), ".pub")
		out = base + "-cert.pub"
	}
	if err := safepath.WriteFile(out, certLine, 0o600); err != nil {
		return fmt.Errorf("write cert %q: %w", out, err)
	}

	errOut := cmd.ErrOrStderr()
	now := time.Now()
	fmt.Fprintf(errOut, "wrote certificate: %s\n", out)
	fmt.Fprintf(errOut, "  key id:     %s\n", keyID)
	fmt.Fprintf(errOut, "  principals: %s\n", strings.Join(principals, ", "))
	fmt.Fprintf(errOut, "  valid:      %s .. %s (%s)\n",
		now.Add(-time.Minute).Format(time.RFC3339),
		now.Add(sshCASignTTL).Format(time.RFC3339),
		sshCASignTTL)
	return nil
}

// trimmedNonEmpty returns vals with surrounding whitespace trimmed and empty
// entries dropped.
func trimmedNonEmpty(vals []string) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		if t := strings.TrimSpace(v); t != "" {
			out = append(out, t)
		}
	}
	return out
}
