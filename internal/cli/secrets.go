package cli

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/cuetry/secrets"
	"github.com/shareed2k/honey/internal/cuetry/secrets/stack"
	"github.com/shareed2k/honey/internal/safepath"
)

var (
	flagSecretsFile        string
	flagSecretsDataKeyFile string
	flagSecretsDataKeyHex  string
	flagSecretsCueKey      string

	flagKeyringService         string
	flagKeyringUser            string
	flagKeyringForce           bool
	flagKeyringInitDataKeyFile string
	flagKeyringInitDataKeyHex  string
)

var secretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Encrypt and decrypt recipe secure:v1 secret refs",
	Long:  "Seal plaintext into secure:v1 refs for CUE recipes, or unseal refs for verification. Uses defaults.secretsprovider and defaults.encryptedkey from honey config.",
}

var secretsSealCmd = &cobra.Command{
	Use:   "seal [plaintext]",
	Short: "Encrypt plaintext to a secure:v1 ref",
	Long: `Reads plaintext from the argument, --file, or stdin, unwraps the stack data key from
honey config (defaults.secretsprovider + defaults.encryptedkey), and prints secure:v1:…
to stdout. Use --cue-key to emit a CUE secrets map entry.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runSecretsSeal,
}

var secretsUnsealCmd = &cobra.Command{
	Use:   "unseal [secure-ref]",
	Short: "Decrypt a secure:v1 ref to plaintext",
	Long: `Reads a secure:v1:… ref from the argument, --file, or stdin and prints the decrypted
plaintext to stdout. Warning: plaintext may appear in shell history and logs.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runSecretsUnseal,
}

var secretsKeyringInitCmd = &cobra.Command{
	Use:   "keyring-init",
	Short: "Create a local OS keyring entry for the stack data key",
	Long: `Generates (or imports) a 32-byte AES stack data key and stores it in the OS credential
store (macOS Keychain, Linux secret service). Prints a YAML snippet to paste into honey config
defaults.secretsprovider. Does not modify config files.`,
	Args: cobra.NoArgs,
	RunE: runSecretsKeyringInit,
}

func init() {
	rootCmd.AddCommand(secretsCmd)
	secretsCmd.PersistentFlags().StringVar(&flagConfig, "config", "", "Path to honey YAML (optional; also HONEY_CONFIG or default paths)")
	secretsCmd.AddCommand(secretsSealCmd, secretsUnsealCmd, secretsKeyringInitCmd)

	secretsKeyringInitCmd.Flags().StringVar(&flagKeyringService, "service", "honey", "Keyring service name (keyring://service/user)")
	secretsKeyringInitCmd.Flags().StringVar(&flagKeyringUser, "user", "stack-data-key", "Keyring account name")
	secretsKeyringInitCmd.Flags().BoolVar(&flagKeyringForce, "force", false, "Overwrite an existing keyring entry")
	secretsKeyringInitCmd.Flags().StringVar(&flagKeyringInitDataKeyFile, "data-key-file", "", "Import 32-byte raw stack key from file instead of generating")
	secretsKeyringInitCmd.Flags().StringVar(&flagKeyringInitDataKeyHex, "data-key-hex", "", "Import 64 hex chars as stack key instead of generating")

	for _, cmd := range []*cobra.Command{secretsSealCmd, secretsUnsealCmd} {
		cmd.Flags().StringVarP(&flagSecretsFile, "file", "f", "", "Read input from file instead of arg/stdin")
		cmd.Flags().StringVar(&flagSecretsDataKeyFile, "data-key-file", "", "Test/dev: 32-byte raw AES stack key file (skips config unwrap)")
		cmd.Flags().StringVar(&flagSecretsDataKeyHex, "data-key-hex", "", "Test/dev: 64 hex chars for 32-byte stack key (skips config unwrap)")
	}
	secretsSealCmd.Flags().StringVar(&flagSecretsCueKey, "cue-key", "", "Print CUE snippet NAME: \"secure:v1:…\" for secrets maps")
}

func runSecretsSeal(cmd *cobra.Command, args []string) error {
	plaintext, err := readSecretsInput(args)
	if err != nil {
		return err
	}
	opts, err := secretsOptionsFromFlags()
	if err != nil {
		return err
	}
	ref, err := secrets.Seal(context.Background(), opts, plaintext)
	if err != nil {
		return err
	}
	if k := strings.TrimSpace(flagSecretsCueKey); k != "" {
		if !secretsEnvNamePattern.MatchString(k) {
			return fmt.Errorf("--cue-key %q must be a POSIX env name", k)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %q\n", k, ref)
		return nil
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), ref)
	return err
}

func runSecretsUnseal(cmd *cobra.Command, args []string) error {
	ref, err := readSecretsInput(args)
	if err != nil {
		return err
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return fmt.Errorf("empty secure ref")
	}
	opts, err := secretsOptionsFromFlags()
	if err != nil {
		return err
	}
	plain, err := secrets.Unseal(context.Background(), opts, ref)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprint(cmd.OutOrStdout(), plain); err != nil {
		return err
	}
	if !strings.HasSuffix(plain, "\n") {
		_, err = fmt.Fprintln(cmd.OutOrStdout())
	}
	return err
}

func runSecretsKeyringInit(cmd *cobra.Command, _ []string) error {
	service := strings.TrimSpace(flagKeyringService)
	user := strings.TrimSpace(flagKeyringUser)
	if service == "" || user == "" {
		return fmt.Errorf("--service and --user must be non-empty")
	}
	exists, err := secrets.KeyringEntryExists(service, user)
	if err != nil {
		return err
	}
	if exists && !flagKeyringForce {
		return fmt.Errorf("keyring entry %s/%s already exists (use --force to overwrite)", service, user)
	}
	key, err := keyringInitDataKey()
	if err != nil {
		return err
	}
	if err := secrets.StoreStackDataKeyInKeyring(service, user, key); err != nil {
		return err
	}
	providerURL := secrets.KeyringProviderURL(service, user)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Stored stack data key in OS keyring: %s\n\n", providerURL)
	_, _ = fmt.Fprint(cmd.OutOrStdout(), secrets.KeyringConfigSnippet(providerURL))
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Paste the block above into your honey config, then run:")
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "  honey secrets seal --config <path> …")
	return nil
}

func keyringInitDataKey() ([]byte, error) {
	hexStr := strings.TrimSpace(flagKeyringInitDataKeyHex)
	filePath := strings.TrimSpace(flagKeyringInitDataKeyFile)
	if hexStr != "" && filePath != "" {
		return nil, fmt.Errorf("use only one of --data-key-hex and --data-key-file")
	}
	if hexStr != "" {
		b, err := hex.DecodeString(hexStr)
		if err != nil {
			return nil, fmt.Errorf("--data-key-hex: %w", err)
		}
		if len(b) != stack.SymmetricKeyBytes {
			return nil, fmt.Errorf("--data-key-hex: want %d bytes, got %d", stack.SymmetricKeyBytes, len(b))
		}
		return b, nil
	}
	if filePath != "" {
		b, err := safepath.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		if len(b) != stack.SymmetricKeyBytes {
			return nil, fmt.Errorf("--data-key-file: want %d bytes, got %d", stack.SymmetricKeyBytes, len(b))
		}
		return b, nil
	}
	return secrets.GenerateStackDataKey()
}

func readSecretsInput(args []string) (string, error) {
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		return args[0], nil
	}
	if p := strings.TrimSpace(flagSecretsFile); p != "" {
		b, err := safepath.ReadFile(p)
		if err != nil {
			return "", err
		}
		return trimTrailingNewline(string(b)), nil
	}
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	return trimTrailingNewline(string(b)), nil
}

func trimTrailingNewline(s string) string {
	s = strings.TrimSuffix(s, "\n")
	s = strings.TrimSuffix(s, "\r")
	return s
}

func secretsOptionsFromFlags() (secrets.Options, error) {
	if dk, err := parseDataKeyOverrides(); err != nil {
		return secrets.Options{}, err
	} else if dk != nil {
		return secrets.Options{SymmetricDataKey: dk}, nil
	}
	cfgPath, err := config.ResolvePath(flagConfig)
	if err != nil {
		return secrets.Options{}, err
	}
	var cfg *config.File
	if cfgPath != "" {
		cfg, err = config.Load(cfgPath)
		if err != nil {
			return secrets.Options{}, fmt.Errorf("config: %w", err)
		}
	}
	o := cuetry.SecretResolverOptionsFromHoney(cfg)
	return secrets.Options{
		SymmetricDataKey: o.SymmetricDataKey,
		SecretsProvider:  o.SecretsProvider,
		EncryptedKey:     o.EncryptedKey,
		AgeIdentityFile:  o.AgeIdentityFile,
	}, nil
}

func parseDataKeyOverrides() ([]byte, error) {
	hexStr := strings.TrimSpace(flagSecretsDataKeyHex)
	filePath := strings.TrimSpace(flagSecretsDataKeyFile)
	if hexStr != "" && filePath != "" {
		return nil, fmt.Errorf("use only one of --data-key-hex and --data-key-file")
	}
	if hexStr != "" {
		b, err := hex.DecodeString(hexStr)
		if err != nil {
			return nil, fmt.Errorf("--data-key-hex: %w", err)
		}
		if len(b) != stack.SymmetricKeyBytes {
			return nil, fmt.Errorf("--data-key-hex: want %d bytes, got %d", stack.SymmetricKeyBytes, len(b))
		}
		return b, nil
	}
	if filePath != "" {
		b, err := safepath.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		if len(b) != stack.SymmetricKeyBytes {
			return nil, fmt.Errorf("--data-key-file: want %d bytes, got %d", stack.SymmetricKeyBytes, len(b))
		}
		return b, nil
	}
	return nil, nil
}

var secretsEnvNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
