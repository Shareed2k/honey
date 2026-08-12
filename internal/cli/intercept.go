package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	"github.com/shareed2k/mogate/pkg/local"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/intercept"
	"github.com/shareed2k/honey/internal/policy"
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
		Use:   "intercept <pod> [-- <command>]",
		Short: "Run a local command whose network and files traverse a target Kubernetes pod",
		Long: `Deploy an OPA-gated, audited interception agent into a target pod and run a
local command whose egress, DNS, incoming traffic, and files traverse that pod.

The command after -- runs locally under the injector; everything before -- is
parsed as flags and the target pod name.

Example:
  honey intercept api-0 -n apps --mode egress -- curl http://internal.svc
  honey intercept api-0 -n apps --mode incoming --target 127.0.0.1:8080 -- ./my-server`,
		Args:          cobra.MinimumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIntercept(cmd, args, resolvedCfg, f)
		},
	}
	flags := cmd.Flags()
	flags.StringVarP(&f.namespace, "namespace", "n", "", "Target pod namespace")
	flags.StringVar(&f.container, "container", "", "Target container the agent shares namespaces with (default: the pod's first container)")
	flags.StringSliceVar(&f.modes, "mode", nil, "Interception modes to enable: egress|incoming|files (repeatable; default from config)")
	flags.StringVar(&f.target, "target", "", "Local application address incoming traffic is forwarded to (host:port; required with --mode incoming)")
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

	pod, command, err := interceptArgs(cmd, args)
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
	if adminURL := strings.TrimRight(strings.TrimSpace(f.adminURL), "/"); adminURL != "" {
		if enabled, defModes, cerr := fetchInterceptConfig(cmd.Context(), adminURL); cerr == nil && enabled {
			return runInterceptBrokered(cmd.Context(), cfg, f, adminURL, defModes, pod, command)
		}
	}

	modeStrs := f.modes
	if len(modeStrs) == 0 {
		modeStrs = ic.DefaultMode
	}
	modes, err := intercept.ParseModes(modeStrs)
	if err != nil {
		return err
	}
	if modes.Incoming && strings.TrimSpace(f.target) == "" {
		return errors.New("intercept: --target is required with --mode incoming")
	}

	agentImage := strings.TrimSpace(f.agentImage)
	if agentImage == "" {
		agentImage = strings.TrimSpace(ic.AgentImage)
	}
	if agentImage == "" {
		return errors.New("intercept: no agent image (set intercept.agent_image or --agent-image)")
	}

	namespace := strings.TrimSpace(f.namespace)
	if namespace == "" {
		return errors.New("intercept: --namespace is required")
	}

	restCfg, err := interceptRestConfig(cfg, f.cluster)
	if err != nil {
		return err
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return fmt.Errorf("intercept: build kubernetes client: %w", err)
	}

	enforcer, err := policy.New(cmd.Context(), ic.PolicyDir, nil)
	if err != nil {
		return fmt.Errorf("intercept: load policy: %w", err)
	}

	sink := gatewayAuditSink(cfg)
	defer func() { _ = sink.Close() }()

	deps := intercept.Deps{
		PortForwarder: &interceptPortForwarder{cfg: restCfg},
		PodExecer:     &interceptPodExecer{cfg: restCfg, clientset: clientset, namespace: namespace, pod: pod},
		K8sClient:     clientset,
		Enforcer:      enforcer,
		Sink:          sink,
		LocalRunner:   intercept.DefaultLocalRunner(),
	}
	opts := intercept.Options{
		Namespace:  namespace,
		Pod:        pod,
		Container:  strings.TrimSpace(f.container),
		Cluster:    strings.TrimSpace(f.cluster),
		AgentImage: agentImage,
		Target:     strings.TrimSpace(f.target),
		Modes:      modes,
		UDP:        f.udp,
		Command:    command,
		Actor:      interceptActor(f.actor),
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
	// by the time we get here. Never logs idToken or resp.Token.
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), interceptStopGrace)
		defer cancel()
		_ = interceptStop(stopCtx, adminURL, resp.SessionID, idToken, nonce)
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
	pf := &interceptPortForwarder{cfg: restCfg}

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

	// The filesystem root offered to remote file operations: only meaningful
	// when Files mode is enabled (mirrors Session.fileRoot).
	root := ""
	if modes.Files {
		root = intercept.DefaultFileRoot
	}

	localCfg := local.Config{
		ControlAddr: controlAddr,
		EgressAddr:  egressAddr,
		Target:      target,
		TokenFile:   tokenFile,
		Socket:      filepath.Join(dir, intercept.RelaySocketName),
		InjectorLib: injectorLib,
		Root:        root,
		UDP:         f.udp,
		Modes:       modes,
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

// interceptArgs splits the positional arguments into the target pod and the
// optional local command. The command is only the arguments after --; cobra
// reports the -- position via ArgsLenAtDash.
func interceptArgs(cmd *cobra.Command, args []string) (pod string, command []string, err error) {
	dash := cmd.ArgsLenAtDash()
	positional := args
	if dash >= 0 {
		positional = args[:dash]
		command = args[dash:]
	}
	if len(positional) != 1 {
		return "", nil, errors.New("intercept: exactly one target pod argument is required (put the command after --)")
	}
	pod = strings.TrimSpace(positional[0])
	if pod == "" {
		return "", nil, errors.New("intercept: target pod name is empty")
	}
	return pod, command, nil
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

// interceptRestConfig resolves the target cluster's REST config. When cluster
// names one of the k8s_proxy clusters, that cluster's kubeconfig/context is
// reused; otherwise the default kubeconfig loading rules apply (KUBECONFIG /
// ~/.kube/config, current context). Reusing k8s_proxy.clusters avoids adding a
// second cluster→kubeconfig mapping to the config.
func interceptRestConfig(cfg *config.File, cluster string) (*rest.Config, error) {
	cluster = strings.TrimSpace(cluster)
	kubeconfig, kubeContext := "", ""
	if cluster != "" {
		// A named cluster MUST resolve to a configured kubeconfig. Silently
		// falling back to the current context would deploy the agent to a
		// different cluster than the one the OPA gate authorized and the audit
		// records — a gate/audit-integrity gap. Error instead.
		found := false
		if cfg.K8sProxy != nil {
			for _, c := range cfg.K8sProxy.Clusters {
				if c.Name == cluster {
					kubeconfig, kubeContext = c.Kubeconfig, c.Context
					found = true
					break
				}
			}
		}
		if !found {
			return nil, fmt.Errorf("intercept: cluster %q is not defined in k8s_proxy.clusters", cluster)
		}
	}
	restCfg, err := k8sprovider.RestConfigForKubeconfig(kubeconfig, kubeContext)
	if err != nil {
		return nil, fmt.Errorf("intercept: resolve cluster %q kubeconfig: %w", cluster, err)
	}
	return restCfg, nil
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

// interceptPortForwarder opens client-go SPDY port-forwards to a target pod,
// binding each to an ephemeral 127.0.0.1 port. It satisfies
// intercept.PortForwarder.
type interceptPortForwarder struct {
	cfg *rest.Config
}

// Forward establishes a port-forward to remotePort on the pod and returns the
// bound local address, a stop function safe to call once, and any setup error.
// The cluster argument is unused: the REST config already targets one cluster.
func (pf *interceptPortForwarder) Forward(ctx context.Context, _, namespace, pod string, remotePort int) (string, func(), error) {
	reqURL, err := url.Parse(fmt.Sprintf("%s/api/v1/namespaces/%s/pods/%s/portforward", pf.cfg.Host, namespace, pod))
	if err != nil {
		return "", nil, fmt.Errorf("intercept: build port-forward url: %w", err)
	}
	transport, upgrader, err := spdy.RoundTripperFor(pf.cfg)
	if err != nil {
		return "", nil, fmt.Errorf("intercept: spdy round tripper: %w", err)
	}
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, reqURL)

	stopCh := make(chan struct{})
	readyCh := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(stopCh) }) }

	fw, err := portforward.New(dialer, []string{fmt.Sprintf("0:%d", remotePort)}, stopCh, readyCh, io.Discard, io.Discard)
	if err != nil {
		stop()
		return "", nil, fmt.Errorf("intercept: create port forwarder: %w", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- fw.ForwardPorts() }()

	select {
	case <-readyCh:
	case err := <-errCh:
		stop()
		return "", nil, fmt.Errorf("intercept: port-forward to %s/%s: %w", namespace, pod, err)
	case <-ctx.Done():
		stop()
		return "", nil, fmt.Errorf("intercept: port-forward to %s/%s: %w", namespace, pod, ctx.Err())
	}

	ports, err := fw.GetPorts()
	if err != nil || len(ports) == 0 {
		stop()
		return "", nil, fmt.Errorf("intercept: resolve local port: %w", err)
	}
	return fmt.Sprintf("127.0.0.1:%d", ports[0].Local), stop, nil
}

// interceptPodExecer delivers the session token into the interception agent by
// executing a command in the pod's agent container. It satisfies
// intercept.PodExecer.
type interceptPodExecer struct {
	cfg       *rest.Config
	clientset kubernetes.Interface
	namespace string
	pod       string
}

// ExecInPod runs cmd in the pod's agent (ephemeral) container, wiring the
// provided streams. The agent container is resolved at exec time because the
// session generates its name at run time and delivers the token without
// threading that name through; the most recently added ephemeral container is
// the session's own agent.
func (e *interceptPodExecer) ExecInPod(ctx context.Context, cmd []string, stdin io.Reader, stdout, stderr io.Writer) error {
	container, err := e.agentContainer(ctx)
	if err != nil {
		return err
	}
	return execInPodContainer(ctx, e.cfg, e.namespace, e.pod, container, cmd, stdin, stdout, stderr)
}

// agentContainer returns the name of the pod's agent container: the most
// recently added ephemeral container, which is this session's agent.
func (e *interceptPodExecer) agentContainer(ctx context.Context) (string, error) {
	p, err := e.clientset.CoreV1().Pods(e.namespace).Get(ctx, e.pod, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("intercept: get pod %q: %w", e.pod, err)
	}
	ecs := p.Spec.EphemeralContainers
	if len(ecs) == 0 {
		return "", fmt.Errorf("intercept: no agent container on pod %q", e.pod)
	}
	return ecs[len(ecs)-1].Name, nil
}
