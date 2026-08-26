package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/shareed2k/mogate/pkg/local"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/intercept"
	"github.com/shareed2k/honey/internal/interceptwire"
	"github.com/shareed2k/honey/internal/provider/k8sprovider"
)

// interceptStopGrace bounds the best-effort interceptStop call the brokered
// path fires on teardown; it uses its own background context (not the
// session's, which may already be cancelled) so the server still hears about
// the stop even when the local command exits via a caller signal.
const interceptStopGrace = 5 * time.Second

// interceptSessionDirPattern is the os.MkdirTemp prefix for the brokered
// path's local session directory, which holds the token file, the relay
// socket, and the extracted injector library — mirroring the direct path's
// own session directory (internal/intercept/session.go).
const interceptSessionDirPattern = "honey-intercept-"

// interceptBrokeredDrainGrace bounds how long the brokered path waits for
// local.Run to unwind after a caller signal cancels the session context.
// Mirrors intercept.Session's own shutdownGrace (internal/intercept/session.go):
// without a bound, a stuck data-plane session could hang the whole process on
// SIGINT/SIGTERM, and since the deferred interceptStop only runs once
// runInterceptBrokered returns, an unbounded hang would also mean the
// interception's server-side teardown never fires.
const interceptBrokeredDrainGrace = 5 * time.Second

// interceptFlags holds the parsed flag values for one honey intercept
// invocation. Bundling them keeps runIntercept's signature stable and lets the
// command bind flags to closure-local variables (so each command instance is
// independent, which the tests rely on).
type interceptFlags struct {
	namespace  string
	container  string
	cluster    string
	agentImage string
	target     string
	actor      string
	modes      []string
	envInclude []string
	envExclude []string
	udp        bool
	adminURL   string
}

func init() {
	rootCmd.AddCommand(newInterceptCmd())
}

// newInterceptCmd builds the honey intercept command. It is a constructor (not
// a package-level singleton) so tests can build an isolated command with its
// own flag state.
func newInterceptCmd() *cobra.Command {
	var f interceptFlags
	cmd := &cobra.Command{
		Use:   "intercept [<pod>] [-- <command>]",
		Short: "Run a local command whose network and files traverse a target Kubernetes pod",
		Long: `Deploy an OPA-gated, audited interception agent and run a local command whose
egress, DNS, incoming traffic, and files traverse it.

The command after -- runs locally under the injector; everything before -- is
parsed as flags and, optionally, the target pod name.

When <pod> is omitted, the session is targetless: a standalone agent (not
attached to any pod) that supports egress and DNS only — no incoming traffic
and no files — and survives target workload redeploys.

Example:
  honey intercept api-0 -n apps --mode egress -- curl http://internal.svc
  honey intercept api-0 -n apps --mode incoming --target 127.0.0.1:8080 -- ./my-server
  honey intercept -n apps -- curl http://internal.svc`,
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIntercept(cmd, args, resolvedCfg, f)
		},
	}
	flags := cmd.Flags()
	flags.StringVarP(&f.namespace, "namespace", "n", "", "Target pod namespace (default: the kubeconfig context's namespace, like kubectl)")
	flags.StringVar(&f.container, "container", "", "Target container the agent shares namespaces with (default: the pod's first container)")
	flags.StringSliceVar(&f.modes, "mode", nil, "Interception modes to enable: egress|incoming|files|env (repeatable; default from config)")
	flags.StringVar(&f.target, "target", "", "Local application address incoming traffic is forwarded to (host:port; required with --mode incoming)")
	flags.StringSliceVar(&f.envInclude, "env-include", nil, "With --mode env, overlay only these target env var names (repeatable; mutually exclusive with --env-exclude)")
	flags.StringSliceVar(&f.envExclude, "env-exclude", nil, "With --mode env, drop these target env var names from the overlay (repeatable; mutually exclusive with --env-include)")
	flags.BoolVar(&f.udp, "udp", false, "Include UDP tunnels alongside TCP")
	flags.StringVar(&f.cluster, "cluster", "", "Target cluster name (resolves kubeconfig/context from k8s_proxy.clusters; default: current kubeconfig context)")
	flags.StringVar(&f.agentImage, "agent-image", "", "Interception agent container image (default from config)")
	flags.StringVar(&f.actor, "actor", "", "Actor recorded in the policy gate and audit log (default: $USER)")
	flags.StringVar(&f.adminURL, "admin-url", defaultKubeAdminURL(), "honey web base URL for server-brokered interception (default $HONEY_WEB_URL)")
	return cmd
}

// runIntercept validates the request against config and flags, wires the real
// cluster dependencies, and runs one interception session under a
// signal-cancelled context. All input validation happens before any cluster is
// touched, so a misconfiguration fails fast and locally.
func runIntercept(cmd *cobra.Command, args []string, cfg *config.File, f interceptFlags) error {
	if cfg == nil || cfg.Intercept == nil || !cfg.Intercept.Enabled {
		return errors.New("intercept is not configured; set intercept.enabled and intercept.agent_image in the honey config")
	}
	ic := cfg.Intercept

	// --env-include and --env-exclude are mutually exclusive: an allow-list and a
	// deny-list on the same overlay are contradictory. Checked up front so both
	// the direct and the brokered path below reject the combination identically.
	if len(f.envInclude) > 0 && len(f.envExclude) > 0 {
		return errors.New("intercept: --env-include and --env-exclude are mutually exclusive; use one or the other")
	}

	pod, targetless, command, err := interceptArgs(cmd, args)
	if err != nil {
		return err
	}

	// Brokered path: when a honey admin URL is configured and reports
	// server-brokered interception enabled, authenticate via SSO and let honey
	// web gate + deploy the agent with its own (service-account) credentials;
	// this process then only signs in, port-forwards with its own more
	// limited credentials, and runs the local injection session. An unset
	// admin URL, a disabled config, or a fetch error all fall through to the
	// direct path below unchanged — a misconfigured or unreachable honey web
	// must not silently block local interception when the operator already
	// has direct cluster credentials.
	//
	// This dispatch MUST run before the direct path's own mode/target
	// validation below: that validation uses the LOCAL intercept.default_mode,
	// but a brokered session validates against the SERVER's default_mode
	// (runInterceptBrokered does its own, independent validation) — the server
	// is authoritative for a brokered session. Validating against the local
	// default first would let a local config with default_mode: [incoming] and
	// no --target hard-error before brokered dispatch is ever reached, even
	// though the server's default mode might not require one.
	//
	// Targetless sessions skip brokered dispatch entirely: server-brokered
	// deploy of a standalone (podless) agent is out of scope for now, so a
	// targetless invocation always takes the direct path below, regardless of
	// whether an admin URL is configured.
	if !targetless {
		if adminURL := strings.TrimRight(strings.TrimSpace(f.adminURL), "/"); adminURL != "" {
			if enabled, defModes, cerr := fetchInterceptConfig(cmd.Context(), adminURL); cerr == nil && enabled {
				return runInterceptBrokered(cmd.Context(), cfg, f, adminURL, defModes, pod, command)
			}
		}
	}

	modes, err := resolveDirectModes(ic, f, targetless)
	if err != nil {
		return err
	}

	agentImage := strings.TrimSpace(f.agentImage)
	if agentImage == "" {
		agentImage = strings.TrimSpace(ic.AgentImage)
	}
	if agentImage == "" {
		return errors.New("intercept: no agent image (set intercept.agent_image or --agent-image)")
	}

	namespace, err := interceptNamespace(cfg, f.cluster, f.namespace)
	if err != nil {
		return err
	}

	restCfg, err := interceptwire.RestConfigForCluster(cfg, f.cluster)
	if err != nil {
		return err
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return fmt.Errorf("intercept: build kubernetes client: %w", err)
	}

	// honey must know the agent pod's name before building Deps, since the
	// execer and port-forwarder bind to it. Targeted mode targets the
	// pre-existing pod named by the positional argument; targetless generates
	// a fresh standalone pod name here, symmetric with the targeted path — so
	// Options.Pod always names "the agent pod", pre-existing or honey-created.
	agentPod := pod
	execContainer := ""
	if targetless {
		agentPod, err = intercept.NewAgentPodName()
		if err != nil {
			return fmt.Errorf("intercept: generate agent pod name: %w", err)
		}
		// The standalone pod's single container has a known, fixed name (unlike
		// the targeted path's ephemeral container, whose name the session picks
		// at run time), so the execer can target it directly.
		execContainer = intercept.AgentContainerName
	}

	deps, sink, err := interceptwire.BuildDeps(cmd.Context(), cfg, restCfg, clientset, namespace, agentPod, execContainer)
	if err != nil {
		return err
	}
	defer func() { _ = sink.Close() }()

	opts := intercept.Options{
		Namespace:  namespace,
		Pod:        agentPod,
		Container:  strings.TrimSpace(f.container),
		Cluster:    strings.TrimSpace(f.cluster),
		AgentImage: agentImage,
		Target:     strings.TrimSpace(f.target),
		Modes:      modes,
		EnvInclude: f.envInclude,
		EnvExclude: f.envExclude,
		UDP:        f.udp,
		Command:    command,
		Actor:      interceptActor(f.actor),
		Targetless: targetless,
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := intercept.New(deps, opts).Run(ctx); err != nil {
		if errors.Is(err, intercept.ErrNoInjector) {
			return fmt.Errorf("%w\nno interception injector is bundled for this platform; on macOS, System Integrity Protection may also block local interception", err)
		}
		return err
	}
	return nil
}

// resolveDirectModes resolves and validates the interception modes for the
// direct path: it falls back to the configured default (and, for a targetless
// session with no default, to egress), parses the names, and enforces the
// path-specific rules — targetless supports only egress (incoming, files, and
// env all need a target pod), and incoming requires a --target.
func resolveDirectModes(ic *config.InterceptConfig, f interceptFlags, targetless bool) (local.Modes, error) {
	modeStrs := f.modes
	if len(modeStrs) == 0 {
		modeStrs = ic.DefaultMode
	}
	if targetless && len(modeStrs) == 0 {
		// A bare `honey intercept -- <command>` (no --mode, no configured
		// default) defaults to egress: targetless supports only egress+DNS, so
		// there is exactly one sensible default, unlike the pod-targeted path
		// where an empty mode set is ambiguous and left as an error.
		modeStrs = []string{"egress"}
	}
	modes, err := intercept.ParseModes(modeStrs)
	if err != nil {
		return local.Modes{}, err
	}
	if targetless {
		if modes.Incoming || modes.Files || modes.Env {
			return local.Modes{}, errors.New("intercept: targetless mode supports only egress; drop --mode incoming/files/env or name a target pod")
		}
	} else if modes.Incoming && strings.TrimSpace(f.target) == "" {
		return local.Modes{}, errors.New("intercept: --target is required with --mode incoming")
	}
	return modes, nil
}

// runInterceptBrokered runs one server-brokered interception: honey web (at
// adminURL) authenticates the caller via SSO, gates the request, deploys the
// interception agent using its own cluster credentials, and hands back a
// per-session token and the two in-agent ports. This process never touches
// the cluster to deploy anything; it signs in, port-forwards to the deployed
// agent with its own (typically more limited) credentials, and runs the local
// injection session exactly as the direct path does. defaultMode is honey
// web's configured default modes (used when --mode is not given), which take
// precedence over any local intercept.default_mode for this call — the server
// is authoritative for a brokered session.
func runInterceptBrokered(ctx context.Context, cfg *config.File, f interceptFlags, adminURL string, defaultMode []string, pod string, command []string) error {
	modeStrs := f.modes
	if len(modeStrs) == 0 {
		modeStrs = defaultMode
	}
	modes, err := intercept.ParseModes(modeStrs)
	if err != nil {
		return err
	}
	target := strings.TrimSpace(f.target)
	if modes.Incoming && target == "" {
		return errors.New("intercept: --target is required with --mode incoming")
	}

	agentImage := strings.TrimSpace(f.agentImage)
	if agentImage == "" {
		agentImage = strings.TrimSpace(cfg.Intercept.AgentImage)
	}
	if agentImage == "" {
		return errors.New("intercept: no agent image (set intercept.agent_image or --agent-image)")
	}

	namespace := strings.TrimSpace(f.namespace)
	if namespace == "" {
		return errors.New("intercept: --namespace is required")
	}
	cluster := strings.TrimSpace(f.cluster)
	container := strings.TrimSpace(f.container)
	// Validate the cluster up front: the brokered path needs it both to route the
	// server-side deploy and to resolve the operator's port-forward credentials.
	// Fail here, before the SSO round-trip and any server-side deploy, rather than
	// after — an empty --cluster otherwise pays for a full authorize+deploy+teardown
	// before the local credential resolver rejects it.
	if cluster == "" {
		return errors.New("intercept: --cluster is required for brokered interception")
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	var scopes []string
	if cfg.OIDC != nil {
		scopes = cfg.OIDC.Scopes
	}
	idToken, nonce, err := browserAuthCodeFlow(ctx, adminURL, scopes)
	if err != nil {
		return fmt.Errorf("intercept: oidc login: %w", err)
	}

	resp, err := interceptAuthorize(ctx, adminURL, idToken, nonce, brokeredAuthorizeReq{
		Cluster:    cluster,
		Namespace:  namespace,
		Pod:        pod,
		Container:  container,
		Mode:       modeStrs,
		UDP:        f.udp,
		Target:     target,
		AgentImage: agentImage,
	})
	if err != nil {
		return fmt.Errorf("intercept: authorize: %w", err)
	}
	// Best-effort teardown: ask honey web to signal the agent to exit and
	// deregister the session on every return path below, including a caller
	// signal (which cancels ctx, unwinds local.Run, and reaches this defer).
	// A fresh background context is used because ctx may already be cancelled
	// by the time we get here. Authenticated by resp.Token (the per-session
	// capability), not idToken: the id_token can expire before a long session
	// ends, but the session token remains valid for exactly this session's
	// lifetime. Never logs idToken or resp.Token.
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), interceptStopGrace)
		defer cancel()
		_ = interceptStop(stopCtx, adminURL, resp.SessionID, resp.Token)
	}()

	// The operator's OWN (typically more limited) cluster credentials, used
	// only to port-forward to the agent honey web already deployed — never to
	// deploy or exec into anything. Prefers the kubeconfig `honey kube login`
	// wrote for this cluster, so no separate local cluster mapping is needed.
	restCfg, credSource, err := brokeredOperatorRestConfig(cfg, cluster)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "intercept: port-forward credentials: %s\n", credSource)
	pf := &interceptwire.PortForwarder{Cfg: restCfg}

	controlAddr, stopControl, err := pf.Forward(ctx, cluster, namespace, pod, resp.ControlPort)
	if err != nil {
		return fmt.Errorf("intercept: forward control port: %w", err)
	}
	defer stopControl()

	egressAddr, stopEgress, err := pf.Forward(ctx, cluster, namespace, pod, resp.EgressPort)
	if err != nil {
		return fmt.Errorf("intercept: forward egress port: %w", err)
	}
	defer stopEgress()

	dir, err := os.MkdirTemp("", interceptSessionDirPattern)
	if err != nil {
		return fmt.Errorf("intercept: create session dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte(resp.Token), 0o600); err != nil {
		return fmt.Errorf("intercept: write token file: %w", err)
	}

	injectorLib, err := intercept.ExtractInjector(dir)
	if err != nil {
		if errors.Is(err, intercept.ErrNoInjector) {
			return fmt.Errorf("%w\nno interception injector is bundled for this platform; on macOS, System Integrity Protection may also block local interception", err)
		}
		return fmt.Errorf("intercept: resolve injector: %w", err)
	}

	// On Apple Silicon a SIP-restricted binary is thinned to its x86_64 slice
	// and run under Rosetta, which loads the x86_64 injector rather than the
	// native arm64 one. Extract it when bundled; a missing x86_64 injector
	// leaves InjectorLibRosetta empty so the data plane fails loud only if a
	// binary actually needs that path — never here.
	var rosettaLib string
	if runtime.GOOS == "darwin" {
		rosettaLib, err = intercept.ExtractRosettaInjector(dir)
		if err != nil {
			if !errors.Is(err, intercept.ErrNoInjector) {
				return fmt.Errorf("intercept: resolve x86_64 injector: %w", err)
			}
			rosettaLib = ""
		}
	}

	// The filesystem root offered to remote file operations: only meaningful
	// when Files mode is enabled (mirrors Session.fileRoot).
	root := ""
	if modes.Files {
		root = intercept.DefaultFileRoot
	}

	localCfg := local.Config{
		ControlAddr:        controlAddr,
		EgressAddr:         egressAddr,
		Target:             target,
		TokenFile:          tokenFile,
		Socket:             filepath.Join(dir, intercept.RelaySocketName),
		InjectorLib:        injectorLib,
		InjectorLibRosetta: rosettaLib,
		Root:               root,
		UDP:                f.udp,
		Modes:              modes,
		EnvInclude:         f.envInclude,
		EnvExclude:         f.envExclude,
	}

	// Bounded drain (mirrors intercept.Session's own drain in session.go): run
	// local.Run inside an errgroup so a caller signal can force its context
	// down and bound the wait to interceptBrokeredDrainGrace instead of
	// blocking forever on a stuck data-plane session. Without this, the
	// deferred interceptStop above — the server-side teardown — would never
	// fire on a hung SIGINT/SIGTERM.
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	eg, egCtx := errgroup.WithContext(runCtx)
	eg.Go(func() error {
		return local.Run(egCtx, localCfg, command)
	})
	return interceptDrainLocalRun(ctx, eg, cancelRun)
}

// interceptDrainLocalRun blocks until eg finishes on its own (the injected
// command exited or local.Run returned), or until ctx is cancelled. On
// cancellation it forces the group's context down via cancel and bounds the
// wait to interceptBrokeredDrainGrace, so the caller (runInterceptBrokered)
// always returns and its deferred teardown — interceptStop, the port-forward
// stops, and the session-dir removal — still runs even if the local injection
// session is stuck. Mirrors intercept.Session's own drain
// (internal/intercept/session.go); logs nothing sensitive.
func interceptDrainLocalRun(ctx context.Context, eg *errgroup.Group, cancel context.CancelFunc) error {
	done := make(chan error, 1)
	go func() { done <- eg.Wait() }()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}

	cancel()
	select {
	case err := <-done:
		return err
	case <-time.After(interceptBrokeredDrainGrace):
		zap.L().Warn("intercept: brokered local session did not exit within the shutdown grace; abandoning wait so teardown can proceed")
		return fmt.Errorf("intercept: shutdown grace exceeded: %w", context.DeadlineExceeded)
	}
}

// interceptArgs splits the positional arguments into the optional target pod
// and the optional local command. The command is only the arguments after
// --; cobra reports the -- position via ArgsLenAtDash.
//
// Zero positional arguments before -- select targetless mode (targetless=true,
// pod=""): a standalone interception session with no target pod. One
// positional argument is the target pod, exactly as before. A literal
// empty-string positional (e.g. `intercept "" -- cmd`) is still an error: the
// caller wrote an explicit (blank) pod argument rather than omitting it.
func interceptArgs(cmd *cobra.Command, args []string) (pod string, targetless bool, command []string, err error) {
	dash := cmd.ArgsLenAtDash()
	positional := args
	if dash >= 0 {
		positional = args[:dash]
		command = args[dash:]
	}
	switch len(positional) {
	case 0:
		return "", true, command, nil
	case 1:
		pod = strings.TrimSpace(positional[0])
		if pod == "" {
			return "", false, nil, errors.New("intercept: target pod name is empty")
		}
		return pod, false, command, nil
	default:
		return "", false, nil, errors.New("intercept: expects at most one target pod argument (put the command after --)")
	}
}

// interceptActor resolves the actor recorded in the gate and audit log: the
// --actor flag when set, otherwise $USER, otherwise "unknown".
func interceptActor(flag string) string {
	if a := strings.TrimSpace(flag); a != "" {
		return a
	}
	if u := strings.TrimSpace(os.Getenv("USER")); u != "" {
		return u
	}
	return "unknown"
}

// interceptNamespace resolves the target namespace like kubectl: an explicit
// --namespace wins; otherwise it defaults to the namespace of the resolved
// kubeconfig context (which itself falls back to "default"), so the operator
// need not pass -n when their context already selects the namespace.
func interceptNamespace(cfg *config.File, cluster, flagNS string) (string, error) {
	if ns := strings.TrimSpace(flagNS); ns != "" {
		return ns, nil
	}
	kubeconfig, kubeContext, err := interceptwire.ClusterKubeconfig(cfg, cluster)
	if err != nil {
		return "", err
	}
	ns, err := k8sprovider.NamespaceForKubeconfig(kubeconfig, kubeContext)
	if err != nil {
		return "", fmt.Errorf("intercept: resolve namespace from kubeconfig: %w", err)
	}
	return ns, nil
}

// brokeredOperatorRestConfig resolves the LOCAL credentials the operator uses to
// port-forward to an agent honey web already deployed on the brokered path. These
// creds only port-forward — they never deploy or exec. Precedence:
//
//  1. an explicit k8s_proxy.clusters entry in the operator's own config, if one
//     names this cluster;
//  2. the "honey-<cluster>" kubeconfig context that `honey kube login <cluster>`
//     writes — port-forwarding then flows through the honey proxy under the
//     operator's impersonated identity, which is exactly where the intercept RBAC
//     split (portforward yes, ephemeralcontainers no) is enforced;
//  3. otherwise an error telling the operator to log in or configure the cluster.
//
// It deliberately does NOT fall back to an arbitrary current kubeconfig context:
// the port-forward must reach the same cluster honey authorized and audited. The
// returned source string names the resolved credential origin for a transparency
// line and carries no secret.
func brokeredOperatorRestConfig(cfg *config.File, cluster string) (*rest.Config, string, error) {
	cluster = strings.TrimSpace(cluster)
	if cluster == "" {
		return nil, "", errors.New("intercept: --cluster is required for brokered interception")
	}
	if cfg != nil && cfg.K8sProxy != nil {
		for _, c := range cfg.K8sProxy.Clusters {
			if c.Name != cluster {
				continue
			}
			// An entry that names the cluster but supplies neither a kubeconfig
			// nor a context would resolve to clientcmd's ambient current-context
			// — the exact silent fallback this resolver forbids. Treat it as no
			// mapping and fall through to the honey kube login context. Cluster
			// names are unique, so stop scanning.
			if strings.TrimSpace(c.Kubeconfig) == "" && strings.TrimSpace(c.Context) == "" {
				break
			}
			rc, err := k8sprovider.RestConfigForKubeconfig(c.Kubeconfig, c.Context)
			if err != nil {
				return nil, "", fmt.Errorf("intercept: resolve cluster %q kubeconfig: %w", cluster, err)
			}
			return rc, fmt.Sprintf("k8s_proxy.clusters[%q]", cluster), nil
		}
	}
	kubeconfigPath := defaultKubeconfigPath()
	loginContext := "honey-" + cluster
	if hasKubeContext(kubeconfigPath, loginContext) {
		rc, err := k8sprovider.RestConfigForKubeconfig(kubeconfigPath, loginContext)
		if err != nil {
			return nil, "", fmt.Errorf("intercept: resolve %q login context: %w", loginContext, err)
		}
		return rc, fmt.Sprintf("honey kube login context %q", loginContext), nil
	}
	return nil, "", fmt.Errorf("intercept: no local credentials for cluster %q — run `honey kube login %s` first, or define it under k8s_proxy.clusters", cluster, cluster)
}

// hasKubeContext reports whether the kubeconfig at path defines a context named
// name. A missing or unreadable kubeconfig reports false.
func hasKubeContext(path, name string) bool {
	cfg, err := loadOrNewKubeconfig(path)
	if err != nil {
		return false
	}
	_, ok := cfg.Contexts[name]
	return ok
}
