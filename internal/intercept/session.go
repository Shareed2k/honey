package intercept

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
	"k8s.io/client-go/kubernetes"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/policy"

	"github.com/shareed2k/mogate/pkg/local"
)

// agentStopGrace bounds the best-effort signal that tells the in-pod agent to
// terminate on teardown so it removes its network redirects. It uses its own
// context (not the session's) so it runs even after the session is cancelled.
const agentStopGrace = 5 * time.Second

// shutdownGrace bounds how long teardown waits for the local injection session
// to unwind after the group context is cancelled. Once it elapses teardown
// stops waiting and force-closes the port-forwards so an interception never
// hangs on a stuck data-plane session.
const shutdownGrace = 5 * time.Second

// ephemeralReadyTimeout bounds how long Run waits for the interception agent's
// ephemeral container to reach the running state (it must pull the image and
// start) before giving up.
const ephemeralReadyTimeout = 60 * time.Second

// agentControlRemotePort is the in-agent control port honey port-forwards to.
const agentControlRemotePort = 30000

// agentEgressRemotePort is the in-agent remote-egress port honey port-forwards
// to.
const agentEgressRemotePort = 30001

// agentContainerPrefix names the interception agent's ephemeral container; a
// random suffix is appended so repeated interceptions on one pod never collide.
const agentContainerPrefix = "mogate"

// agentSubcommand is the agent binary subcommand that installs the in-pod
// redirects and serves the capture and egress ports.
const agentSubcommand = "kube-agent"

// agentNameRandomBytes is the number of random bytes drawn for the ephemeral
// container name suffix (hex-encoded, so twice this many characters).
const agentNameRandomBytes = 6

// sessionDirPattern is the os.MkdirTemp prefix for a session's 0700 temporary
// directory, which holds the token file, the relay socket, and the extracted
// injector library.
const sessionDirPattern = "honey-intercept-"

// RelaySocketName is the basename of the relay Unix socket inside a session
// directory. The socket is created mode 0600 by the local session. Exported so
// the CLI's brokered intercept path (internal/cli/intercept.go), which builds
// its own session directory instead of going through Session, uses the same
// name rather than a duplicated literal.
const RelaySocketName = "relay.sock"

// DefaultFileRoot is the filesystem root offered to remote file operations
// when the Files mode is enabled. An empty root disables file redirection.
// Exported for the same reason as RelaySocketName.
const DefaultFileRoot = "/"

// PortForwarder opens a local TCP port-forward to a remote port on a target pod
// and returns the local address to dial, a stop function that tears the
// forward down, and any error establishing it. Implementations must make stop
// safe to call exactly once.
type PortForwarder interface {
	// Forward establishes a port-forward to remotePort on the named pod and
	// returns the local address, a stop function, and any setup error.
	Forward(ctx context.Context, cluster, namespace, pod string, remotePort int) (localAddr string, stop func(), err error)
}

// LocalRunner runs one local injection session: it dials the port-forwarded
// control and egress addresses, reads the session token from the token file,
// and runs command with the injector loaded, returning when command exits or
// ctx is cancelled. It is the single seam to the data-plane dependency.
type LocalRunner interface {
	// Run executes command under the local injection session described by cfg.
	Run(ctx context.Context, cfg local.Config, command []string) error
}

// Deps are the collaborators a Session needs. Every field is an interface (or a
// standard client type with a fake) so a Session is exercised without a real
// cluster or a real data-plane session.
type Deps struct {
	// PortForwarder opens the control and egress port-forwards to the agent.
	PortForwarder PortForwarder
	// PodExecer delivers the session token into the running agent.
	PodExecer PodExecer
	// K8sClient applies the ephemeral container and polls it to running.
	K8sClient kubernetes.Interface
	// Enforcer authorizes the interception; a nil enforcer fails closed.
	Enforcer *policy.Enforcer
	// Sink receives the intercept_start and intercept_stop audit events; it may
	// be nil, in which case auditing is skipped.
	Sink audit.Sink
	// LocalRunner runs the local injection session.
	LocalRunner LocalRunner
}

// Options describes one interception request: which pod and container to
// target, the operator-configured agent image, the interception modes, and the
// local command to run under injection.
type Options struct {
	// Namespace is the target pod's namespace.
	Namespace string
	// Pod is the target pod name.
	Pod string
	// Container is the target container the agent shares namespaces with.
	Container string
	// Cluster is the target cluster name (used for gating, audit, and
	// port-forwarding).
	Cluster string
	// AgentImage is the operator-configured interception agent image.
	AgentImage string
	// Target is the local application address that incoming steal traffic is
	// forwarded to. It is required when Modes.Incoming is set.
	Target string
	// Modes selects which interception capabilities are active.
	Modes local.Modes
	// EnvInclude, when non-empty, restricts the env-mode overlay to these
	// target environment keys (after the built-in and EnvExclude filters). It is
	// mutually exclusive with EnvExclude and only meaningful when Modes.Env is
	// set. It carries key names only, never values, so it is safe to record.
	EnvInclude []string
	// EnvExclude names additional target environment keys to drop from the
	// env-mode overlay, on top of the data plane's built-in defaults. It is
	// mutually exclusive with EnvInclude and only meaningful when Modes.Env is
	// set. It carries key names only, never values.
	EnvExclude []string
	// UDP includes the UDP tunnels alongside TCP.
	UDP bool
	// Command is the local command run under injection.
	Command []string
	// Actor is the authenticated subject requesting the interception.
	Actor string
	// InjectorLib optionally overrides the injector library used for local
	// injection. When empty (the default), Run extracts the platform library
	// bundled with honey. When set, Run uses this path verbatim instead of
	// extracting the embedded library — for operators who ship a prebuilt
	// injector out of band, and for tests that inject a freshly built library.
	InjectorLib string
	// InjectorLibRosetta optionally overrides the x86_64 injector library used
	// for SIP-patched binaries thinned to their x86_64 slice (run under Rosetta
	// on Apple Silicon). When empty on darwin, Run extracts the bundled x86_64
	// library if one is present; when no x86_64 injector is available the SIP
	// x86_64 path fails loud at use. It is ignored off darwin.
	InjectorLibRosetta string
	// Targetless deploys a standalone agent Pod instead of an ephemeral
	// container in a target pod; egress+DNS only, no Pod.
	Targetless bool

	// agentPrivileged runs the interception agent's ephemeral container with a
	// privileged security context (privileged, uid 0, gid agentBypassGID) instead
	// of the default NET_ADMIN-only context. It exists only for the end-to-end
	// test running against a nested k3s (Docker-in-Docker), where NET_ADMIN is not
	// reliably propagated to the agent so it cannot program the network namespace.
	// It is unexported, so production callers keep the default NET_ADMIN-only
	// context; the zero value leaves production behaviour unchanged.
	agentPrivileged bool
}

// Session orchestrates a single OPA-gated, audited interception: it gates the
// request, deploys the agent as an ephemeral container, delivers a per-session
// token, port-forwards the agent's control and egress ports, and runs the local
// command under injection, tearing everything down on return.
type Session struct {
	deps Deps
	opts Options
}

// New constructs a Session from its dependencies and options.
func New(deps Deps, opts Options) *Session {
	return &Session{deps: deps, opts: opts}
}

// Live is a deployed interception agent ready to serve local commands: the
// port-forwards are open, the token is delivered, and the per-command
// local.Config template is resolved. It is produced by Establish and torn
// down by Close; between those two calls Run may be invoked any number of
// times, each running one command through the same deployed agent.
type Live struct {
	// session owns the Deps and Options this Live was established from, and
	// is what Close reports the stop audit event through (s.deps.Sink,
	// s.stopEvent).
	session *Session

	// cfg is the per-command local.Config template: everything Establish
	// resolved once (addresses, token file, injector paths, mode/env
	// settings) except Socket, which Run sets fresh per call.
	cfg local.Config

	// dir is the session's temporary directory (token file, injector,
	// per-command relay sockets).
	dir string

	// cleanups accumulates teardown steps in acquisition order; Close runs
	// them in reverse.
	cleanups []func()

	// socketSeq is an atomic counter giving each Run call's relay socket a
	// unique name under dir, so serial reuse of one Live never collides with
	// mogate's per-run create/remove of the socket file.
	socketSeq atomic.Uint64

	// start is when Establish began, used by Close to compute the audited
	// session duration.
	start time.Time

	// closeOnce guards teardown so a double Close (for example the
	// back-compat Run's defer racing an Establish-error path) runs the
	// cleanups and the stop audit exactly once.
	closeOnce sync.Once
}

// Establish gates and audits the interception request, deploys the agent
// (ephemeral container or standalone pod), delivers the session token, and
// opens the port-forward(s), returning a Live ready for repeated Run calls.
// It does not run any command and does not drain or tear anything down on
// success. A gate denial short-circuits before any deploy and is not
// audited. If any later step fails, Establish runs the cleanups it has
// accumulated so far and records the stop audit event before returning the
// error, so a failed Establish leaves nothing deployed behind.
func (s *Session) Establish(ctx context.Context) (live *Live, err error) {
	if gateErr := gate(ctx, s.deps.Enforcer, s.gateInput()); gateErr != nil {
		return nil, gateErr
	}

	start := time.Now()
	auditStart(ctx, s.deps.Sink, s.startEvent())

	l := &Live{session: s, start: start}
	defer func() {
		if err != nil {
			l.Close(stopReason(err))
		}
	}()

	dir, err := s.newSessionDir()
	if err != nil {
		return nil, err
	}
	l.dir = dir
	l.cleanups = append(l.cleanups, func() { _ = os.RemoveAll(dir) })

	injectorLib, err := s.resolveInjector(dir)
	if err != nil {
		return nil, fmt.Errorf("intercept: resolve injector: %w", err)
	}
	rosettaLib, err := s.resolveRosettaInjector(dir)
	if err != nil {
		return nil, fmt.Errorf("intercept: resolve x86_64 injector: %w", err)
	}

	token, err := mintToken()
	if err != nil {
		return nil, err
	}
	tokenFile, err := writeTokenFile(dir, token)
	if err != nil {
		return nil, err
	}

	var controlAddr, egressAddr string
	if s.opts.Targetless {
		egressAddr, err = s.provisionTargetless(ctx, token, &l.cleanups)
	} else {
		controlAddr, egressAddr, err = s.provisionTargeted(ctx, token, &l.cleanups)
	}
	if err != nil {
		return nil, err
	}

	l.cfg = local.Config{
		ControlAddr:        controlAddr,
		EgressAddr:         egressAddr,
		Target:             s.opts.Target,
		TokenFile:          tokenFile,
		InjectorLib:        injectorLib,
		InjectorLibRosetta: rosettaLib,
		Root:               s.fileRoot(),
		UDP:                s.opts.UDP,
		Modes:              s.opts.Modes,
		EnvInclude:         s.opts.EnvInclude,
		EnvExclude:         s.opts.EnvExclude,
	}

	return l, nil
}

// Run runs one command through the already-deployed agent, blocking until it
// exits or ctx is cancelled. Each call gets its own relay socket path under
// l.dir so sequential Run calls on one Live never collide over mogate's
// per-run create/remove of the socket file. The runner's error is returned
// verbatim (wrapped only with %w where wrapped at all) so callers can
// errors.As into it, for example to recover an *exec.ExitError.
func (l *Live) Run(ctx context.Context, runner LocalRunner, command []string) error {
	cfg := l.cfg
	cfg.Socket = l.socketPath()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	eg, egCtx := errgroup.WithContext(runCtx)
	eg.Go(func() error {
		return runner.Run(egCtx, cfg, command)
	})

	return drain(ctx, eg, cancel)
}

// socketPath returns a fresh relay socket path under l.dir for one Run call.
// The first call uses the well-known RelaySocketName, matching the single
// command every existing caller runs through the back-compat Run(ctx); each
// later call on the same Live appends an incrementing counter so it never
// collides with mogate's per-run create+remove of the socket file.
func (l *Live) socketPath() string {
	n := l.socketSeq.Add(1) - 1
	if n == 0 {
		return filepath.Join(l.dir, RelaySocketName)
	}
	return filepath.Join(l.dir, fmt.Sprintf("relay-%d.sock", n))
}

// Close tears the Live down: it runs the accumulated cleanups in reverse
// acquisition order (stopping the port-forwards, best-effort agent
// teardown, then removing the session directory) and records the stop audit
// event with reason and the elapsed duration since Establish. It is
// idempotent — a second Close (for example from the back-compat Run's defer
// after Establish already closed on its own error path) is a no-op.
func (l *Live) Close(reason string) {
	l.closeOnce.Do(func() {
		for i := len(l.cleanups) - 1; i >= 0; i-- {
			l.cleanups[i]()
		}
		auditStop(context.Background(), l.session.deps.Sink, l.session.stopEvent(time.Since(l.start), reason))
	})
}

// Run executes the interception lifecycle and blocks until the injected
// command exits or ctx is cancelled. It is the back-compat composition of
// Establish, Live.Run, and Live.Close for callers that only ever run one
// command per session (the CLI and web callers): it gates and audits the
// request, deploys and waits for the agent, delivers the token, opens the
// port-forward(s), and runs the command under a bounded-drain group. On
// return it drains the group within shutdownGrace, then force-closes the
// port-forwards, removes the session directory, and records the stop audit
// event. A gate denial short-circuits before any deploy and is not audited.
func (s *Session) Run(ctx context.Context) (err error) {
	live, err := s.Establish(ctx)
	if err != nil {
		return err
	}
	defer func() { live.Close(stopReason(err)) }()
	return live.Run(ctx, s.deps.LocalRunner, s.opts.Command)
}

// provisionTargeted deploys the interception agent as an ephemeral container
// in the pre-existing target pod (s.opts.Pod), waits for it to reach the
// running state, delivers the session token, and opens both the control and
// egress port-forwards. It appends its cleanups — the best-effort in-agent
// SIGTERM and the two port-forward stops — to *cleanups in acquisition order,
// so Run's teardown unwinds them in reverse regardless of where in this
// sequence a later step fails.
func (s *Session) provisionTargeted(ctx context.Context, token string, cleanups *[]func()) (controlAddr, egressAddr string, err error) {
	agentName, err := agentContainerName()
	if err != nil {
		return "", "", err
	}
	ec := ephemeralContainer(agentName, s.opts.AgentImage, s.opts.Container, agentArgs(s.opts.UDP), s.opts.Modes.Env)
	if s.opts.agentPrivileged {
		elevateEphemeralPrivilege(&ec)
	}
	if err := applyEphemeral(ctx, s.deps.K8sClient, s.opts.Namespace, s.opts.Pod, ec); err != nil {
		return "", "", err
	}
	// Best-effort: on teardown, signal the agent (PID 1 of its container) to
	// terminate so it runs its graceful shutdown and removes its network
	// redirects — Kubernetes cannot delete the ephemeral container, so without
	// this the target pod's traffic could stay redirected after an incoming
	// session. Uses a fresh bounded context (the session context may already be
	// cancelled) and ExecInPod, which is independent of the port-forwards that
	// teardown closes. The agent image must exec the agent as PID 1 to receive it.
	*cleanups = append(*cleanups, func() {
		stopCtx, cancelStop := context.WithTimeout(context.Background(), agentStopGrace)
		defer cancelStop()
		_ = s.deps.PodExecer.ExecInPod(stopCtx, []string{"sh", "-c", "kill -TERM 1 2>/dev/null || true"}, nil, io.Discard, io.Discard)
	})
	if err := waitEphemeralRunning(ctx, s.deps.K8sClient, s.opts.Namespace, s.opts.Pod, agentName, ephemeralReadyTimeout); err != nil {
		return "", "", err
	}
	if err := deliverToken(ctx, s.deps.PodExecer, token); err != nil {
		return "", "", err
	}

	controlAddr, stopControl, err := s.deps.PortForwarder.Forward(ctx, s.opts.Cluster, s.opts.Namespace, s.opts.Pod, agentControlRemotePort)
	if err != nil {
		return "", "", fmt.Errorf("intercept: forward control port: %w", err)
	}
	*cleanups = append(*cleanups, stopControl)

	egressAddr, stopEgress, err := s.deps.PortForwarder.Forward(ctx, s.opts.Cluster, s.opts.Namespace, s.opts.Pod, agentEgressRemotePort)
	if err != nil {
		return "", "", fmt.Errorf("intercept: forward egress port: %w", err)
	}
	*cleanups = append(*cleanups, stopEgress)

	return controlAddr, egressAddr, nil
}

// provisionTargetless deploys the interception agent as a standalone,
// unprivileged Pod named s.opts.Pod (a name the CLI generated before Deps was
// built, symmetric with the targeted path where the pod pre-exists), waits
// for it to reach the running state, delivers the session token, and opens
// the egress-only port-forward. There is no control port-forward in
// targetless mode: the standalone Pod has no target namespace to steal
// incoming traffic from, so ControlAddr is left empty in the caller's
// local.Config. On success it appends the standalone Pod's deletion to
// *cleanups (best-effort, bounded by its own fresh context since the session
// context may already be cancelled by the time teardown runs) and the egress
// port-forward's stop, in acquisition order.
func (s *Session) provisionTargetless(ctx context.Context, token string, cleanups *[]func()) (egressAddr string, err error) {
	pod := standaloneAgentPod(s.opts.Pod, s.opts.Namespace, s.opts.AgentImage, standaloneAgentArgs(s.opts.UDP))
	if err := createAgentPod(ctx, s.deps.K8sClient, s.opts.Namespace, pod); err != nil {
		return "", err
	}
	// Best-effort: unlike the targeted path's ephemeral container (which
	// Kubernetes cannot delete), the standalone Pod is entirely honey's own —
	// tearing it down means deleting it outright rather than asking it to exit.
	*cleanups = append(*cleanups, func() {
		stopCtx, cancelStop := context.WithTimeout(context.Background(), agentStopGrace)
		defer cancelStop()
		_ = deleteAgentPod(stopCtx, s.deps.K8sClient, s.opts.Namespace, s.opts.Pod)
	})
	if err := waitPodRunning(ctx, s.deps.K8sClient, s.opts.Namespace, s.opts.Pod, ephemeralReadyTimeout); err != nil {
		return "", err
	}
	if err := deliverToken(ctx, s.deps.PodExecer, token); err != nil {
		return "", err
	}

	egressAddr, stopEgress, err := s.deps.PortForwarder.Forward(ctx, s.opts.Cluster, s.opts.Namespace, s.opts.Pod, agentEgressRemotePort)
	if err != nil {
		return "", fmt.Errorf("intercept: forward egress port: %w", err)
	}
	*cleanups = append(*cleanups, stopEgress)

	return egressAddr, nil
}

// drain blocks until the group finishes on its own (the command exited or the
// runner returned), or until ctx is cancelled. On cancellation it forces the
// group context down and waits at most shutdownGrace for the runner to unwind,
// returning a deadline error if the grace elapses first.
func drain(ctx context.Context, eg *errgroup.Group, cancel context.CancelFunc) error {
	done := make(chan error, 1)
	go func() { done <- eg.Wait() }()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}

	// The caller cancelled ctx (for example SIGINT): tear the group down and
	// bound the drain so teardown never blocks on a stuck runner.
	cancel()
	select {
	case err := <-done:
		return err
	case <-time.After(shutdownGrace):
		return fmt.Errorf("intercept: shutdown grace exceeded: %w", context.DeadlineExceeded)
	}
}

// newSessionDir creates the session's temporary directory. os.MkdirTemp already
// creates it with mode 0700 (owner-only), which is what this dir needs — it
// holds the token file, the relay socket, and the injector — so no further
// chmod is required.
func (s *Session) newSessionDir() (string, error) {
	dir, err := os.MkdirTemp("", sessionDirPattern)
	if err != nil {
		return "", fmt.Errorf("intercept: create session dir: %w", err)
	}
	return dir, nil
}

// resolveInjector returns the injector library path for this session. When
// Options.InjectorLib is set it is used verbatim (after confirming it exists),
// so an operator-supplied or test-supplied library takes precedence over the
// embedded one. Otherwise the platform library bundled with honey is extracted
// into dir. Keeping the override ahead of extraction means production behaviour
// is unchanged whenever InjectorLib is empty.
func (s *Session) resolveInjector(dir string) (string, error) {
	if s.opts.InjectorLib != "" {
		if _, err := os.Stat(s.opts.InjectorLib); err != nil {
			return "", fmt.Errorf("injector library %q: %w", s.opts.InjectorLib, err)
		}
		return s.opts.InjectorLib, nil
	}
	return extractInjector(dir)
}

// resolveRosettaInjector returns the x86_64 injector path for this session, or
// "" when none is needed or available. SIP-patching a restricted system binary
// on Apple Silicon thins it to its x86_64 slice and runs it under Rosetta, which
// must load the x86_64 injector rather than the native arm64 one. Off darwin
// there is no SIP/Rosetta path, so it returns "". An operator/test override
// takes precedence; otherwise the bundled x86_64 library is extracted, and a
// missing one (ErrNoInjector) yields "" so the data plane fails loud only if a
// binary actually needs the x86_64 path — never here.
func (s *Session) resolveRosettaInjector(dir string) (string, error) {
	if s.opts.InjectorLibRosetta != "" {
		if _, err := os.Stat(s.opts.InjectorLibRosetta); err != nil {
			return "", fmt.Errorf("x86_64 injector library %q: %w", s.opts.InjectorLibRosetta, err)
		}
		return s.opts.InjectorLibRosetta, nil
	}
	if runtime.GOOS != "darwin" {
		return "", nil
	}
	lib, err := ExtractRosettaInjector(dir)
	if err != nil {
		if errors.Is(err, ErrNoInjector) {
			return "", nil
		}
		return "", err
	}
	return lib, nil
}

// gateInput builds the OPA authorization input for this session.
func (s *Session) gateInput() GateInput {
	return GateInput{
		Actor:      s.opts.Actor,
		Cluster:    s.opts.Cluster,
		Namespace:  s.opts.Namespace,
		Pod:        s.opts.Pod,
		Container:  s.opts.Container,
		Mode:       s.modeStrings(),
		AgentImage: s.opts.AgentImage,
	}
}

// startEvent builds the audit payload for the interception start.
func (s *Session) startEvent() Event {
	return Event{
		Actor:      s.opts.Actor,
		Cluster:    s.opts.Cluster,
		Namespace:  s.opts.Namespace,
		Pod:        s.opts.Pod,
		Container:  s.opts.Container,
		Mode:       s.modeStrings(),
		AgentImage: s.opts.AgentImage,
	}
}

// stopEvent builds the audit payload for the interception stop, adding the
// run duration and the stop reason.
func (s *Session) stopEvent(d time.Duration, reason string) Event {
	ev := s.startEvent()
	ev.Duration = d
	ev.Reason = reason
	return ev
}

// modeStrings renders the active modes as the string slice the gate and audit
// payloads carry.
func (s *Session) modeStrings() []string {
	return modeStrings(s.opts.Modes)
}

// modeStrings renders m as the string slice the gate and audit payloads
// carry.
func modeStrings(m local.Modes) []string {
	var out []string
	if m.Egress {
		out = append(out, "egress")
	}
	if m.Incoming {
		out = append(out, "incoming")
	}
	if m.Files {
		out = append(out, "files")
	}
	if m.Env {
		out = append(out, "env")
	}
	return out
}

// fileRoot returns the filesystem root offered to remote file operations: the
// default root when Files mode is enabled, otherwise empty (redirection off).
func (s *Session) fileRoot() string {
	if s.opts.Modes.Files {
		return DefaultFileRoot
	}
	return ""
}

// agentContainerName returns a fresh ephemeral-container name of the form
// "<prefix>-<random hex>". The random suffix avoids collisions across repeated
// interceptions on the same pod.
func agentContainerName() (string, error) {
	b := make([]byte, agentNameRandomBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("intercept: generate agent container name: %w", err)
	}
	return agentContainerPrefix + "-" + hex.EncodeToString(b), nil
}

// agentArgs builds the agent container arguments: the subcommand, the in-agent
// token file path the token is delivered to, and the UDP toggle. The token
// value itself is delivered out of band and never appears on the command line.
func agentArgs(udp bool) []string {
	return []string{
		agentSubcommand,
		"--token-file", agentRunDir + "/" + tokenFileName,
		fmt.Sprintf("--udp=%t", udp),
	}
}

// stopReason maps the session's terminal error to a concise, token-free audit
// reason.
func stopReason(err error) string {
	switch {
	case err == nil:
		return "completed"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return err.Error()
	}
}
