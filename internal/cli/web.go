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
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/intercept"
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

	// Server-brokered honey intercept: built (fail-closed) only when
	// intercept.enabled and a k8s_proxy cluster registry are both configured.
	interceptBroker, interceptModes, err := buildInterceptBroker(cfg, authCfg.enforcer, auditSink)
	if err != nil {
		return err
	}
	// Reuse the Broker's session store for the browser-interception registry so
	// brokered and browser interceptions share ONE registry (the same-pod guard
	// then sees both). nil when no Broker: the web server owns an in-memory one.
	var interceptStore intercept.SessionStore
	if interceptBroker != nil {
		interceptStore = interceptBroker.Store()
	}

	// Browser interception terminal (GET /ws/intercept): a DIRECT intercept
	// Session run on the honey-web host, wired with the same enforcer and audit
	// sink the broker uses. Built alongside the broker (same enable conditions),
	// but keyed off the pod record itself rather than a --cluster flag: the web UI
	// hands over the k8s record it already searched.
	var interceptSessionFactory func(hosts.Record, intercept.Options, intercept.LocalRunner) (*intercept.Session, error)
	if interceptBroker != nil {
		enforcer := authCfg.enforcer
		interceptSessionFactory = func(rec hosts.Record, opts intercept.Options, runner intercept.LocalRunner) (*intercept.Session, error) {
			restCfg, rerr := interceptWebRestConfig(cfg, rec)
			if rerr != nil {
				return nil, rerr
			}
			clientset, cerr := kubernetes.NewForConfig(restCfg)
			if cerr != nil {
				return nil, fmt.Errorf("intercept: build kubernetes client: %w", cerr)
			}
			deps := intercept.Deps{
				PortForwarder: &interceptPortForwarder{cfg: restCfg},
				PodExecer:     &interceptPodExecer{cfg: restCfg, clientset: clientset, namespace: opts.Namespace, pod: opts.Pod},
				K8sClient:     clientset,
				Enforcer:      enforcer,
				Sink:          auditSink,
				LocalRunner:   runner,
			}
			return intercept.New(deps, opts), nil
		}
	}

	// Non-secret OIDC values the login command echoes back to clients. Kept out
	// of the webserver package so it needn't import config for these strings.
	var oidcPublic *webserver.OIDCPublicConfig
	if cfg != nil && cfg.OIDC != nil {
		oidcPublic = &webserver.OIDCPublicConfig{
			Issuer:   cfg.OIDC.Issuer,
			ClientID: cfg.OIDC.ClientID,
			Scopes:   cfg.OIDC.Scopes,
		}
	}

	srv, err := webserver.NewServer(webserver.Options{
		ListenAddr:              webListen,
		Token:                   token,
		DisableAuth:             disableAuth,
		ConfigPath:              cfgPath,
		Config:                  cfg,
		ExecRegistry:            buildHostExecRegistry(),
		SearchRegistry:          GetSearchRegistry(),
		RecordDir:               recordDir,
		LocalFilesRoot:          webFilesRoot,
		AgentBinaryPath:         webAgentBin,
		AgentBuildCacheDir:      webAgentBuildCacheDir,
		Version:                 BuildVersion(),
		Commit:                  BuildCommit(),
		Date:                    BuildDate(),
		MetricsListenAddr:       strings.TrimSpace(webMetricsListen),
		Metrics:                 prom,
		NoCache:                 flagNoCache,
		Refresh:                 flagRefresh,
		AllowLogsCommand:        webAllowLogsCommand,
		OnReady:                 onReady,
		Enforcer:                authCfg.enforcer,
		Guardrails:              guardrailRules,
		JWTPubKey:               authCfg.jwtPubKey,
		TrustedProxyNets:        authCfg.trustedNets,
		WebAuthn:                authCfg.webauthn,
		EnableMesh:              meshnet.Enabled(),
		AuditSink:               auditSink,
		K8sProxy:                k8sProxyCfg,
		OIDCVerifier:            authCfg.oidcVerifier,
		OIDCPublic:              oidcPublic,
		DeviceCertTTL:           cfg.DeviceCertTTLValue(),
		InterceptBroker:         interceptBroker,
		InterceptDefaultMode:    interceptModes,
		InterceptSessionFactory: interceptSessionFactory,
		InterceptStore:          interceptStore,
	})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if interceptBroker != nil {
		// Reaps any brokered session past its TTL (e.g. a crashed CLI never
		// called interceptStop) so an interception — especially incoming-mode
		// network redirects — cannot outlive its session unboundedly. Exits on
		// ctx cancellation; the returned "done" channel is intentionally
		// ignored here since nothing needs to wait on it.
		interceptBroker.StartJanitor(ctx)
	}
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

// buildInterceptBroker builds the server-side interception Broker from
// cfg.Intercept. It resolves one *rest.Config per cluster straight from
// cfg.K8sProxy.Clusters using the same builder (k8sprovider.RestConfigFor
// Kubeconfig) that buildK8sProxyServerConfig uses for the inbound access
// proxy — honey's own service-account credentials, the same trust boundary —
// but it is a separate resolution, not a shared k8sproxy.Registry object: the
// Broker needs a raw *rest.Config per cluster (to build a kubernetes.Interface
// and a brokerPodExecer), not the proxy's impersonation-aware registry. It
// returns (nil, nil, nil) when brokered interception is disabled (no
// intercept block, intercept.enabled is false, or no k8s_proxy clusters are
// configured), so honey web's brokered intercept routes stay unregistered and
// honey intercept remains client-side only. Any cluster whose
// kubeconfig/context fails to resolve is a hard startup error (fail-closed),
// consistent with the proxy. It also selects (and, for sqlite/postgres,
// opens) the session store per cfg.Intercept.SessionStoreValue — see
// buildInterceptSessionStore — before the Broker is constructed, so a
// misconfigured or unreachable store fails honey web startup rather than
// silently falling back to an in-memory store.
func buildInterceptBroker(cfg *config.File, enforcer *policy.Enforcer, sink audit.Sink) (*intercept.Broker, []string, error) {
	if cfg == nil || cfg.Intercept == nil || !cfg.Intercept.Enabled || cfg.K8sProxy == nil || len(cfg.K8sProxy.Clusters) == 0 {
		return nil, nil, nil
	}

	// The store outlives this call (the Broker holds it for the life of the
	// process), so it is not bound to any request/command context.
	store, err := buildInterceptSessionStore(context.Background(), cfg.Intercept)
	if err != nil {
		return nil, nil, err
	}

	restCfgs := make(map[string]*rest.Config, len(cfg.K8sProxy.Clusters))
	for _, c := range cfg.K8sProxy.Clusters {
		restCfg, err := k8sprovider.RestConfigForKubeconfig(c.Kubeconfig, c.Context)
		if err != nil {
			return nil, nil, fmt.Errorf("intercept: cluster %q: %w", c.Name, err)
		}
		restCfgs[c.Name] = restCfg
	}

	deps := intercept.BrokerDeps{
		Clientset: func(cluster string) (kubernetes.Interface, error) {
			restCfg, ok := restCfgs[cluster]
			if !ok {
				return nil, fmt.Errorf("intercept: unknown cluster %q", cluster)
			}
			return kubernetes.NewForConfig(restCfg)
		},
		Execer: func(cluster, ns, pod, container string) (intercept.PodExecer, error) {
			restCfg, ok := restCfgs[cluster]
			if !ok {
				return nil, fmt.Errorf("intercept: unknown cluster %q", cluster)
			}
			return &brokerPodExecer{cfg: restCfg, ns: ns, pod: pod, container: container}, nil
		},
		Enforcer:   enforcer,
		Sink:       sink,
		SessionTTL: cfg.Intercept.SessionTTLValue(),
		Store:      store,
	}
	return intercept.NewBroker(deps), cfg.Intercept.DefaultMode, nil
}

// interceptWebRestConfig resolves the cluster REST config for a browser-driven
// interception on a Kubernetes pod record. It prefers a k8s_proxy.clusters entry
// whose name or context matches the record's kube_context — honey's own
// configured credentials — and otherwise falls back to the record's own
// kubeconfig + kube_context meta, the same resolution the k8s exec/terminal path
// uses for a pod record (internal/provider/k8sprovider/executor.go). The direct
// Session runs with whatever credentials this resolves, so on the default local
// topology it is the operator's own kubeconfig.
func interceptWebRestConfig(cfg *config.File, rec hosts.Record) (*rest.Config, error) {
	kubeContext := strings.TrimSpace(rec.Meta["kube_context"])
	if cfg != nil && cfg.K8sProxy != nil {
		for _, c := range cfg.K8sProxy.Clusters {
			if c.Name == kubeContext || strings.TrimSpace(c.Context) == kubeContext {
				restCfg, err := k8sprovider.RestConfigForKubeconfig(c.Kubeconfig, c.Context)
				if err != nil {
					return nil, fmt.Errorf("intercept: resolve cluster %q kubeconfig: %w", kubeContext, err)
				}
				return restCfg, nil
			}
		}
	}
	restCfg, err := k8sprovider.RestConfigForKubeconfig(strings.TrimSpace(rec.Meta["kubeconfig"]), kubeContext)
	if err != nil {
		return nil, fmt.Errorf("intercept: resolve pod kubeconfig: %w", err)
	}
	return restCfg, nil
}

// buildInterceptSessionStore selects the backing store for brokered intercept
// sessions per ic.SessionStoreValue(): "memory" (the default) returns
// (nil, nil) since intercept.NewBroker already treats a nil Store as an
// in-memory one; "sqlite" and "postgres" open a persistent database/sql store
// (intercept.NewSQLStore) against ic.SessionStoreDSN. It fails closed rather
// than falling back to memory: an unknown store value, an empty DSN for
// sqlite/postgres, or a NewSQLStore open error are all returned as errors, so
// a misconfigured store stops honey web from starting instead of silently
// running without persistence. The DSN is never logged.
func buildInterceptSessionStore(ctx context.Context, ic *config.InterceptConfig) (intercept.SessionStore, error) {
	switch store := ic.SessionStoreValue(); store {
	case "memory":
		return nil, nil
	case "sqlite", "postgres":
		dsn := strings.TrimSpace(ic.SessionStoreDSN)
		if dsn == "" {
			return nil, fmt.Errorf("intercept: session_store %q requires session_store_dsn", store)
		}
		driver := "sqlite3"
		if store == "postgres" {
			driver = "pgx"
		}
		s, err := intercept.NewSQLStore(ctx, driver, dsn)
		if err != nil {
			return nil, fmt.Errorf("intercept: open session store: %w", err)
		}
		return s, nil
	default:
		return nil, fmt.Errorf("intercept: unknown session_store %q", store)
	}
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
