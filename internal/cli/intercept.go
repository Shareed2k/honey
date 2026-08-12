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
	"strings"
	"sync"
	"syscall"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/intercept"
	"github.com/shareed2k/honey/internal/policy"
	"github.com/shareed2k/honey/internal/provider/k8sprovider"
)

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
	client := &k8sprovider.K8sNativeClient{
		Config:    e.cfg,
		Clientset: e.clientset,
		Namespace: e.namespace,
		PodName:   e.pod,
		Container: container,
	}
	return client.ExecInPod(ctx, cmd, stdin, stdout, stderr, false, nil)
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
