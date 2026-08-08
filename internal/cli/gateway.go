package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostapi"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/policy"
	"github.com/shareed2k/honey/internal/safepath"
	"github.com/shareed2k/honey/internal/sshca"
	"github.com/shareed2k/honey/internal/sshgateway"
)

// recordsCacheTTL bounds how long the gateway reuses a host-search result before
// re-searching, so every inbound connection does not trigger a full inventory
// scan.
const recordsCacheTTL = 30 * time.Second

var (
	gwListen     string
	gwHostKeyDir string
	gwTrustedCA  []string
	gwUserAttr   string
	gwCertAttr   string
	gwNoAuth     bool
)

var gatewayCmd = &cobra.Command{
	Use:   "ssh-server",
	Short: "Start the inbound SSH gateway (certificate-authenticated shell/exec proxy to inventory hosts)",
	Long: `Runs an inbound SSH server. Native ssh clients authenticate with an SSH
certificate signed by a configured trusted CA; the certificate maps to a honey
actor, the first ssh command argument selects an inventory host, and the session
is proxied to that host — recorded, policy-gated, and audited.

Examples:
  ssh -t -i alice-cert.pub alice@127.0.0.1 -p 12222 <resource>      # interactive shell
  ssh alice@127.0.0.1 -p 12222 <resource> uptime                   # ad-hoc command
  echo "select 1" | ssh alice@127.0.0.1 -p 12222 <resource> psql   # stdin pipe`,
	Args: cobra.NoArgs,
	RunE: runGateway,
}

func init() {
	gatewayCmd.Flags().StringVar(&gwListen, "listen", "localhost:12222", "Listen address (host:port)")
	gatewayCmd.Flags().StringVar(&gwHostKeyDir, "host-key", "", "Directory to load/create the gateway SSH host key (default: state dir)")
	gatewayCmd.Flags().StringArrayVar(&gwTrustedCA, "trusted-ca", nil, "Path to a trusted CA public key file (repeatable)")
	gatewayCmd.Flags().StringVar(&gwUserAttr, "user-attr", "principal", "Identity attribute label recorded for audit")
	gatewayCmd.Flags().StringVar(&gwCertAttr, "cert-attr", "principal", "Certificate field used as the actor: principal or key_id")
	gatewayCmd.Flags().BoolVar(&gwNoAuth, "no-auth", false, "Disable certificate authentication (dev only; accepts any client)")
	rootCmd.AddCommand(gatewayCmd)
}

func runGateway(cmd *cobra.Command, _ []string) error {
	cfg := resolvedCfg
	cfgPath := resolvedCfgPath
	gwCfg := gatewaySettings(cfg)

	listen := firstNonEmptyString(gwListen, gwCfg.Listen, "localhost:12222")
	userAttr := firstNonEmptyString(gwUserAttr, gwCfg.UserAttr, "principal")
	certAttr := firstNonEmptyString(gwCertAttr, gwCfg.CertAttr, "principal")

	hostKey, err := loadGatewayHostKey(firstNonEmptyString(gwHostKeyDir, gwCfg.HostKey, ""))
	if err != nil {
		return err
	}

	trustedPaths := gwTrustedCA
	if len(trustedPaths) == 0 {
		trustedPaths = gwCfg.TrustedCA
	}
	var trustedCAs []ssh.PublicKey
	if !gwNoAuth {
		trustedCAs, err = parseTrustedCAFiles(trustedPaths)
		if err != nil {
			return err
		}
		if len(trustedCAs) == 0 {
			// No explicit --trusted-ca / config CAs: auto-trust the built-in SSH
			// CA if `honey ssh-ca init` has created one under the state dir (the
			// same dir the gateway host key uses), so no manual --trusted-ca is
			// needed after init.
			stateDir, derr := gatewayStateDir(firstNonEmptyString(gwHostKeyDir, gwCfg.HostKey, ""))
			if derr != nil {
				return derr
			}
			caPub, ok, cerr := sshca.LoadCAPublicKey(stateDir)
			if cerr != nil {
				return cerr
			}
			if ok {
				trustedCAs = append(trustedCAs, caPub)
				_, _ = fmt.Fprintf(os.Stdout, "Trusting built-in ssh-ca from %s\n", stateDir)
			}
		}
		if len(trustedCAs) == 0 {
			return fmt.Errorf("no trusted CA configured: run `honey ssh-ca init`, pass --trusted-ca <file> (or set ssh_gateway.trusted_ca), or --no-auth for dev")
		}
	}

	enforcer, err := gatewayEnforcer(cmd.Context(), cfg, gwCfg.PolicyDir)
	if err != nil {
		return err
	}

	sink := gatewayAuditSink(cfg)
	defer func() { _ = sink.Close() }()

	recordDir := ""
	if gwCfg.Record {
		recordDir = config.ResolveRecordDir(cfg, cfgPath, flagRecordDir, recordDirFlagChanged(cmd))
	}

	provider := newRecordsProvider(cfg, cfgPath)

	maskRules, err := sshgateway.NewMaskRuleset(gwCfg.Mask.Values, gwCfg.Mask.Patterns)
	if err != nil {
		return fmt.Errorf("ssh_gateway.mask: %w", err)
	}

	var defaultSSHUser string
	if cfg != nil {
		defaultSSHUser = cfg.Defaults.SSHUser
	}

	srv, err := sshgateway.New(sshgateway.Options{
		ListenAddr:     listen,
		HostKey:        hostKey,
		TrustedCAs:     trustedCAs,
		UserAttr:       userAttr,
		CertAttr:       certAttr,
		DefaultSSHUser: defaultSSHUser,
		Enforcer:       enforcer,
		AuditSink:      sink,
		RecordDir:      recordDir,
		Records:        provider,
		MaskRules:      maskRules,
		DisableAuth:    gwNoAuth,
	})
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(os.Stdout, "\nHoney SSH gateway (Ctrl+C to stop)\n  Listen: %s\n", listen)
	if gwNoAuth {
		_, _ = fmt.Fprintf(os.Stdout, "  AUTH:   DISABLED (--no-auth) — only expose on a trusted network\n")
	} else {
		_, _ = fmt.Fprintf(os.Stdout, "  AUTH:   SSH certificate (trusted CAs: %d)\n", len(trustedCAs))
	}
	if recordDir != "" {
		_, _ = fmt.Fprintf(os.Stdout, "  Record: %s\n", recordDir)
	}
	_, _ = fmt.Fprintln(os.Stdout)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return srv.Start(ctx)
}

// gatewaySettings returns the SSHGateway config block, or a zero value when unset.
func gatewaySettings(cfg *config.File) config.SSHGatewayConfig {
	if cfg == nil || cfg.SSHGateway == nil {
		return config.SSHGatewayConfig{}
	}
	return *cfg.SSHGateway
}

// loadGatewayHostKey loads or creates the gateway host key. dir defaults to the
// resolved state dir when empty.
func loadGatewayHostKey(dir string) (ssh.Signer, error) {
	resolved, err := gatewayStateDir(dir)
	if err != nil {
		return nil, err
	}
	return sshgateway.LoadOrCreateHostKey(resolved)
}

// gatewayStateDir resolves the gateway state dir: dir when set, else the honey
// state dir. This is the directory used both for the host key and for the
// built-in SSH CA (see internal/sshca) that the gateway auto-trusts.
func gatewayStateDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		sd, err := config.ResolveStateDir()
		if err != nil {
			return "", fmt.Errorf("resolve state dir: %w", err)
		}
		dir = sd
	}
	return dir, nil
}

// parseTrustedCAFiles reads each authorized-keys-format CA public key file and
// returns the parsed public keys (multiple keys per file are supported).
func parseTrustedCAFiles(paths []string) ([]ssh.PublicKey, error) {
	var keys []ssh.PublicKey
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		raw, err := safepath.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read trusted CA %q: %w", p, err)
		}
		rest := bytes.TrimSpace(raw)
		for len(rest) > 0 {
			key, _, _, remaining, perr := ssh.ParseAuthorizedKey(rest)
			if perr != nil {
				return nil, fmt.Errorf("parse trusted CA %q: %w", p, perr)
			}
			keys = append(keys, key)
			rest = bytes.TrimSpace(remaining)
		}
	}
	return keys, nil
}

// gatewayEnforcer builds the OPA enforcer. A gateway-specific policy_dir takes
// precedence; otherwise it falls back to the shared web auth resolution
// (HONEY_POLICY_DIR / defaults.policy_dir).
func gatewayEnforcer(ctx context.Context, cfg *config.File, policyDir string) (*policy.Enforcer, error) {
	if dir := strings.TrimSpace(policyDir); dir != "" {
		data, err := inventoryData(cfg)
		if err != nil {
			return nil, fmt.Errorf("policy_dir: inventory data: %w", err)
		}
		enf, err := policy.New(ctx, dir, data)
		if err != nil {
			return nil, fmt.Errorf("ssh_gateway.policy_dir: %w", err)
		}
		return enf, nil
	}
	authCfg, err := resolveWebAuthConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return authCfg.enforcer, nil
}

// gatewayAuditSink builds the audit sink from config (mirrors the web server).
func gatewayAuditSink(cfg *config.File) audit.Sink {
	if cfg != nil && cfg.Audit.Enabled {
		path := cfg.Audit.EffectivePath()
		if s, err := audit.NewFileSink(path); err == nil {
			return s
		}
	}
	return audit.NewNoopSink()
}

// newRecordsProvider returns a records provider that runs the standard host
// search and caches the result for a short TTL, so each inbound connection does
// not re-search the whole inventory.
func newRecordsProvider(cfg *config.File, cfgPath string) func(ctx context.Context) ([]hosts.Record, error) {
	var (
		mu     sync.Mutex
		cached []hosts.Record
		expiry time.Time
	)
	return func(ctx context.Context) ([]hosts.Record, error) {
		mu.Lock()
		defer mu.Unlock()
		if time.Now().Before(expiry) && cached != nil {
			return cached, nil
		}
		in := hostapi.SearchHostsInput{
			ConfigPath: cfgPath,
			Config:     cfg,
		}
		out, err := hostapi.SearchHosts(ctx, &in, buildHostExecRegistry(), GetSearchRegistry())
		if err != nil {
			return nil, err
		}
		cached = out.Records
		expiry = time.Now().Add(recordsCacheTTL)
		return cached, nil
	}
}

func firstNonEmptyString(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
