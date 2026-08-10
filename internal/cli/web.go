package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/k8sproxy"
	"github.com/shareed2k/honey/internal/meshnet"
	"github.com/shareed2k/honey/internal/metrics"
	"github.com/shareed2k/honey/internal/policy"
	"github.com/shareed2k/honey/internal/provider/k8sprovider"
	"github.com/shareed2k/honey/internal/webserver"
)

var (
	webListen             string
	webFilesRoot          string
	webAgentBin           string
	webAgentBuildCacheDir string
	webMetricsListen      string
	webAllowLogsCommand   bool
	webBrowser            bool
	webNoAuth             bool
)

var webCmd = &cobra.Command{
	Use:   "web",
	Short: "Start embedded web UI (token-protected) for backends, search, config, SSH terminal, and uploads",
	Args:  cobra.NoArgs,
	RunE:  runWeb,
}

func init() {
	webCmd.Flags().StringVar(&webListen, "listen", "localhost:8765", "Listen address (host:port)")
	webCmd.Flags().StringVar(&webFilesRoot, "files-root", "", "Local filesystem root for the web file browser (default: $HONEY_FILES_ROOT or $HOME)")
	webCmd.Flags().StringVar(&webAgentBin, "agent-bin", "", "Explicit path to honey-transfer-agent binary (optional)")
	webCmd.Flags().StringVar(&webAgentBuildCacheDir, "agent-build-cache-dir", "", "Directory used to cache auto-built honey-transfer-agent binary")
	webCmd.Flags().StringVar(&webMetricsListen, "metrics-listen", "", "Optional host:port for Prometheus /metrics (e.g. 127.0.0.1:9091)")
	webCmd.Flags().BoolVar(&webAllowLogsCommand, "allow-logs-command", false, "Allow callers to run arbitrary remote commands via the logs streaming endpoint (disabled by default)")
	webCmd.Flags().BoolVar(&webBrowser, "browser", true, "Open the web UI in the default browser on start")
	webCmd.Flags().BoolVar(&webNoAuth, "no-auth", false, "Disable web UI token authentication (only for trusted networks / behind an authenticating proxy; also via HONEY_WEB_NO_AUTH)")
	rootCmd.AddCommand(webCmd)
}

func runWeb(cmd *cobra.Command, _ []string) error {
	// Mesh (internal/meshnet) is started for every honey subcommand in
	// root.go's PersistentPreRunE, which always runs before this RunE — so
	// by this point Start has already been attempted if cfg.Mesh.Enabled.
	// Stop it (best-effort, log-only on error) on every return path out of
	// this function, not just the srv.Start(ctx) exit at the bottom: a
	// prior version of this function only stopped mesh after a successful
	// webserver.NewServer + srv.Start, which meant an early return (e.g.
	// webserver.NewServer itself failing) leaked the running mesh Host.
	// meshnet.Enabled() is false (a no-op) whenever mesh wasn't started, so
	// this defer is always safe to register unconditionally.
	defer stopMeshBestEffort()

	disableAuth := webNoAuth
	if !disableAuth {
		if b, perr := strconv.ParseBool(strings.TrimSpace(os.Getenv("HONEY_WEB_NO_AUTH"))); perr == nil {
			disableAuth = b
		}
	}
	// Resolve a stable token (persisted to the state dir so it survives restarts).
	stateDir, _ := config.ResolveStateDir()
	token, err := webserver.ResolveToken(stateDir)
	if err != nil {
		return err
	}
	cfgPath := resolvedCfgPath
	cfg := resolvedCfg
	recordDir := config.ResolveRecordDir(cfg, cfgPath, flagRecordDir, recordDirFlagChanged(cmd))
	// Make the resolved record dir explicit in the config defaults for this
	// session so the Config screen shows it and async webhook runs are recorded
	// (and thus retrievable via the webhook results endpoint).
	if cfg != nil && strings.TrimSpace(cfg.Defaults.RecordDir) == "" {
		cfg.Defaults.RecordDir = recordDir
	}
	var prom *metrics.Registry
	if strings.TrimSpace(webMetricsListen) != "" {
		prom = metrics.NewRegistry(BuildVersion(), BuildCommit())
	}
	url := fmt.Sprintf("http://%s/?token=%s", webListen, token)
	_, _ = fmt.Fprintf(os.Stdout, "\nHoney Web UI (Ctrl+C to stop)\n  URL:   %s\n  API:   Authorization: Bearer <token>  or  X-Honey-Token: <token>\n  WS:    /ws/ssh?token=<token>\n  Assist: OPENAI_API_KEY (+ optional OPENAI_BASE_URL)\n", url)
	if disableAuth {
		_, _ = fmt.Fprintf(os.Stdout, "  AUTH:  DISABLED (--no-auth) — only expose on a trusted network or behind an authenticating proxy\n")
	} else if strings.TrimSpace(os.Getenv("HONEY_WEB_TOKEN")) == "" && strings.TrimSpace(stateDir) != "" {
		_, _ = fmt.Fprintf(os.Stdout, "  Token: persisted in %s/web_token (stable across restarts; set HONEY_WEB_TOKEN to override)\n", stateDir)
	}
	if strings.TrimSpace(webMetricsListen) != "" {
		_, _ = fmt.Fprintf(os.Stdout, "  Metrics: http://%s/metrics\n", webMetricsListen)
	}
	_, _ = fmt.Fprintln(os.Stdout)

	var onReady func()
	if webBrowser {
		onReady = func() { _ = openBrowser(url) }
	}

	// Use the command context: resolveWebAuthConfig performs OIDC discovery
	// (network I/O) when cfg.OIDC is set, and startup should honor cancellation.
	authCfg, err := resolveWebAuthConfig(cmd.Context(), cfg)
	if err != nil {
		return fmt.Errorf("web auth config: %w", err)
	}

	guardrailRules, err := buildGuardrailRuleset(cfg)
	if err != nil {
		return err
	}

	// One audit sink, shared by the web server and the k8s access proxy so both
	// append to the same log. Closed on return (mirrors the gateway command).
	auditSink := gatewayAuditSink(cfg)
	defer func() { _ = auditSink.Close() }()

	// Inbound Kubernetes access proxy: built (fail-closed) only when configured.
	k8sProxyCfg, err := buildK8sProxyServerConfig(cfg, authCfg.enforcer, auditSink)
	if err != nil {
		return err
	}

	srv, err := webserver.NewServer(webserver.Options{
		ListenAddr:         webListen,
		Token:              token,
		DisableAuth:        disableAuth,
		ConfigPath:         cfgPath,
		Config:             cfg,
		ExecRegistry:       buildHostExecRegistry(),
		SearchRegistry:     GetSearchRegistry(),
		RecordDir:          recordDir,
		LocalFilesRoot:     webFilesRoot,
		AgentBinaryPath:    webAgentBin,
		AgentBuildCacheDir: webAgentBuildCacheDir,
		Version:            BuildVersion(),
		Commit:             BuildCommit(),
		Date:               BuildDate(),
		MetricsListenAddr:  strings.TrimSpace(webMetricsListen),
		Metrics:            prom,
		NoCache:            flagNoCache,
		Refresh:            flagRefresh,
		AllowLogsCommand:   webAllowLogsCommand,
		OnReady:            onReady,
		Enforcer:           authCfg.enforcer,
		Guardrails:         guardrailRules,
		JWTPubKey:          authCfg.jwtPubKey,
		TrustedProxyNets:   authCfg.trustedNets,
		WebAuthn:           authCfg.webauthn,
		EnableMesh:         meshnet.Enabled(),
		AuditSink:          auditSink,
		K8sProxy:           k8sProxyCfg,
		OIDCVerifier:       authCfg.oidcVerifier,
		DeviceCertTTL:      cfg.DeviceCertTTLValue(),
	})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return srv.Start(ctx)
}

// buildK8sProxyServerConfig builds the inbound Kubernetes access proxy's server
// config from cfg.K8sProxy. It returns (nil, nil) when the block is absent so
// the proxy stays disabled. Any cluster whose kubeconfig/context or registry
// fails to resolve is a hard startup error (fail-closed): honey never starts the
// boundary in a partially-configured state.
func buildK8sProxyServerConfig(cfg *config.File, enforcer *policy.Enforcer, sink audit.Sink) (*k8sproxy.ServerConfig, error) {
	if cfg == nil || cfg.K8sProxy == nil {
		return nil, nil
	}
	kp := cfg.K8sProxy

	specs := make([]k8sproxy.ClusterSpec, 0, len(kp.Clusters))
	for _, c := range kp.Clusters {
		restCfg, err := k8sprovider.RestConfigForKubeconfig(c.Kubeconfig, c.Context)
		if err != nil {
			return nil, fmt.Errorf("k8s_proxy: cluster %q: %w", c.Name, err)
		}
		specs = append(specs, k8sproxy.ClusterSpec{
			Name:          c.Name,
			Config:        restCfg,
			UserFrom:      c.Impersonate.UserFrom,
			DefaultGroups: c.Impersonate.DefaultGroups,
			Labels:        c.Labels,
		})
	}

	reg, err := k8sproxy.NewRegistry(specs)
	if err != nil {
		return nil, fmt.Errorf("k8s_proxy: %w", err)
	}

	return &k8sproxy.ServerConfig{
		Listen:          kp.Listen,
		ServingCertPath: kp.TLSCert,
		ServingKeyPath:  kp.TLSKey,
		ClientCAPath:    kp.ClientCA,
		Registry:        reg,
		Enforcer:        enforcer,
		AuditSink:       sink,
	}, nil
}

// stopMeshBestEffort stops the mesh singleton (internal/meshnet) if it was
// started, logging (not returning) any Stop error — mirrors the log-and-continue
// pattern this file already uses for mesh startup problems. A no-op when mesh
// was never enabled/started.
func stopMeshBestEffort() {
	if !meshnet.Enabled() {
		return
	}
	if err := meshnet.Stop(context.Background()); err != nil {
		zap.L().Warn("honey mesh failed to stop cleanly", zap.Error(err))
	}
}
