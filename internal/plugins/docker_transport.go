package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	containertypes "github.com/moby/moby/api/types/container"
	networktypes "github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"go.uber.org/zap"

	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
)

const (
	pluginInitContainerPort = 49094
	pluginInitBindPath      = "/honey-plugin-init"
)

// pluginInitPort is pluginInitContainerPort/tcp as a network.Port, used both
// to expose the container port and to look up its published host-side
// binding. Published to an ephemeral 127.0.0.1 host port (see createAndStart
// and waitForReady) rather than dialed directly by container IP: a
// container's Docker-bridge IP is only host-routable when the Docker daemon
// runs natively on the host's own network stack (plain Linux). On any
// VM-backed setup (Docker Desktop, Colima, Lima, ...) — common on macOS/
// Windows dev machines and confirmed on this session's own Colima-backed
// host — that bridge subnet has no route from outside the VM, so dialing the
// container's internal IP hangs until the caller's context deadline.
// Publishing the port and dialing loopback works uniformly across all of
// these.
var pluginInitPort = networktypes.MustParsePort(fmt.Sprintf("%d/tcp", pluginInitContainerPort))

// DockerBackend abstracts the daemon a docker plugin's shim-container runs
// against: the operator's local Docker daemon (localBackend, below) or a
// remote host's daemon over SSH (the engine's ssh backend). It captures
// exactly the three operator-local assumptions of the shim model — which
// daemon, where the honey-plugin-init binary lives to bind-mount, and how to
// reach the shim's published loopback port. Everything else in
// dockerTransport (container lifecycle, the /call HTTP protocol) is
// backend-agnostic, so local and remote share one code path.
type DockerBackend interface {
	// Client returns the moby client for the target daemon. The transport
	// does not close it — Close (below) owns that.
	Client() *client.Client
	// ShimHostPath returns the path, on the daemon host, of the
	// honey-plugin-init binary to bind-mount as the container entrypoint.
	// Remote backends stage the binary onto the host here (idempotently).
	ShimHostPath(ctx context.Context) (string, error)
	// DialShim dials the shim's published loopback address on the daemon
	// host. Local dials directly; remote tunnels through SSH. The signature
	// matches http.Transport.DialContext so it can be wired straight in.
	DialShim(ctx context.Context, network, address string) (net.Conn, error)
	// Close releases backend-owned resources (the moby client, etc.). It
	// must not close a borrowed/shared SSH connection it does not own.
	Close() error
}

// localBackend is the default DockerBackend: the operator's own Docker daemon,
// the honey-plugin-init binary resolved on the operator's filesystem, and a
// plain loopback dial. Behavior identical to the pre-backend code path.
type localBackend struct {
	shimPath string
	cli      *client.Client
}

// newLocalBackend builds a localBackend against the ambient Docker daemon
// (DOCKER_HOST/env via client.FromEnv). socket, when non-empty, overrides the
// daemon host (unused by the plugin loader today; kept for symmetry with the
// docker step's own socket override).
func newLocalBackend(shimPath, socket string) (*localBackend, error) {
	var (
		cli *client.Client
		err error
	)
	if socket != "" {
		cli, err = client.New(client.FromEnv, client.WithHost(socket))
	} else {
		cli, err = client.New(client.FromEnv)
	}
	if err != nil {
		return nil, fmt.Errorf("plugins: docker client: %w", err)
	}
	return &localBackend{shimPath: shimPath, cli: cli}, nil
}

func (b *localBackend) Client() *client.Client { return b.cli }

func (b *localBackend) ShimHostPath(context.Context) (string, error) { return b.shimPath, nil }

func (b *localBackend) DialShim(ctx context.Context, network, address string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, address)
}

func (b *localBackend) Close() error { return b.cli.Close() }

// freeLoopbackPortAllocator is an optional DockerBackend capability: it returns
// a currently-free TCP port on the daemon host's loopback, for a host-network
// plugin whose shim cannot use a published port mapping. It is a separate,
// type-asserted interface (not a DockerBackend method) so DockerBackend stays
// small — a bigger interface is a weaker abstraction.
type freeLoopbackPortAllocator interface {
	FreeLoopbackPort(ctx context.Context) (int, error)
}

var _ freeLoopbackPortAllocator = (*localBackend)(nil)

// FreeLoopbackPort binds an ephemeral loopback port, reads it, and releases it.
// The window between release and the container binding it is a benign TOCTOU:
// if the port is taken in between, readiness simply times out and the operator
// retries — not worth a lock for a rare, host-network-only path.
func (b *localBackend) FreeLoopbackPort(_ context.Context) (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("plugins: allocate free loopback port: %w", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// dockerTransport runs a plugin as a long-lived Docker container (for the
// Manager's process lifetime, or a DockerHostSession's run lifetime for
// remote hosts — not one container per call), reached over HTTP via the
// honey-plugin-init binary bind-mounted as its entrypoint. The target daemon,
// shim binary location, and shim dialer all come from backend.
type dockerTransport struct {
	backend    DockerBackend
	cue        *pluginCue
	httpClient *http.Client
	createCfg  dockerTransportConfig

	mu          sync.RWMutex
	containerID string
	addr        string // http://<ip>:49094
	restarting  bool

	stopWatch chan struct{}

	// cancel cancels the transport's own internally-derived context (see
	// newDockerTransport), independent of whatever ctx the caller passed in
	// at construction time. Close calls it so an in-progress restart backoff
	// (docker_restart.go) is interrupted even if the caller's context (e.g.
	// context.Background()) never gets cancelled on its own.
	cancel context.CancelFunc

	// lifecycleCtx is the same internally-derived context as cancel guards,
	// stored so ensureStarted can hand it to startWatching regardless of
	// which per-call ctx triggered the first-use start — a per-call ctx is
	// cancelled once that call returns, which would otherwise kill the
	// crash-watch goroutine immediately after the very first call.
	lifecycleCtx context.Context

	// startMu guards lazy first-use startup: a plugin discovered on disk
	// (manifest.yaml present) should not pull/create/start a container until
	// a recipe actually calls it — many discovered plugins (e.g. the aws/
	// gcloud/duckdb example plugins) are never referenced by a given run, and
	// eagerly starting every one of them left stray running containers and
	// required a reachable Docker daemon just to load plugins the run never
	// touches. started stays false until the first successful ensureStarted.
	startMu sync.Mutex
	started bool

	// onStarted is called once, after a successful first-use start, with the
	// transport's lifecycleCtx — production always sets this to
	// t.startWatching (newDockerTransport). A seam so unit tests can exercise
	// ensureStarted's create/dedup/retry logic without spawning the real
	// crash-watch goroutine, which calls the live Docker API and would
	// violate this package's no-real-Docker-daemon unit test convention. Left
	// nil, ensureStarted simply skips watching (used only by tests that
	// construct a dockerTransport directly).
	onStarted func(context.Context)
}

func (t *dockerTransport) setRestarting(v bool) {
	t.mu.Lock()
	t.restarting = v
	t.mu.Unlock()
}

func (t *dockerTransport) isRestarting() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.restarting
}

func (t *dockerTransport) currentAddr() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.addr
}

// dockerTransportConfig holds everything needed to create+start the container.
// The honey-plugin-init binary path is not here — it comes from the backend's
// ShimHostPath (local resolves it on the operator FS; remote stages it onto
// the target host), since it differs per daemon.
type dockerTransportConfig struct {
	Image      string
	PullPolicy string // "if_not_present" or "always"
	CueSource  []byte
	MaxBackoff time.Duration
	Env        map[string]string // resolved allowed_env values, passed through as container env vars
	Volumes    []string          // static bind mounts from manifest.Docker.Volumes, Docker bind syntax
	InitMode   string            // "bind" or "embedded" (never empty; resolved in loadDockerPluginDir)
	InitPath   string            // in-image honey-plugin-init path when InitMode=="embedded"

	// KeepWarm opts this plugin's container into cross-run reuse: it is created
	// with a deterministic name + labels (see docker_warmpool.go), a later run
	// attaches to it instead of creating a new one, and Close leaves it running
	// (reaped by `honey plugins gc`). PluginID is the manifest id, used to name
	// the container. Both come from plugins.keep_warm; unset → today's behavior
	// (anonymous container, removed on Close).
	KeepWarm bool
	PluginID string

	// HostNetwork runs the container with NetworkMode "host" instead of the
	// default bridge network — no ports are exposed/published, and the shim
	// is instead told (via buildContainerConfig's Cmd) to bind an
	// operator-allocated loopback port directly. Set from
	// manifest.Docker.Network == "host" in loadDockerPluginDir, which is
	// itself gated on plugins.allow_host_network (see manager.go). Host
	// networking grants the container the daemon host's full network
	// namespace, so this is never the default.
	HostNetwork bool
}

// newDockerTransport validates the plugin's cue source and wires the shim HTTP
// client to dial through backend, but does not pull/create/start a container —
// that's deferred to ensureStarted on the transport's first real call (see
// dockerTransport.started). Takes ownership of backend: it is Closed here on a
// construction error and by dockerTransport.Close otherwise.
func newDockerTransport(ctx context.Context, backend DockerBackend, cfg dockerTransportConfig) (*dockerTransport, error) {
	pc, err := newPluginCue(cfg.CueSource)
	if err != nil {
		backend.Close()
		return nil, err
	}

	// internalCtx is derived from the caller's ctx but owned by the
	// transport itself: Close cancels it directly (see Close below) so the
	// watch/restart lifecycle can always be interrupted, regardless of
	// whether the caller's own ctx ever gets cancelled.
	internalCtx, cancel := context.WithCancel(ctx)
	dt := &dockerTransport{
		backend:      backend,
		cue:          pc,
		httpClient:   &http.Client{Transport: &http.Transport{DialContext: backend.DialShim}},
		createCfg:    cfg,
		cancel:       cancel,
		lifecycleCtx: internalCtx,
	}
	dt.onStarted = dt.startWatching
	return dt, nil
}

// ensureStarted pulls/creates/starts the plugin's container on first use and
// begins crash-watching it. A no-op on every call after the first successful
// start. createFn is a seam so tests can exercise this without a real Docker
// daemon; production callers always pass a closure over createAndStart.
// startMu (not the RWMutex guarding containerID/addr) serializes this so
// concurrent first calls to the same plugin don't race to create two
// containers — a failed attempt is not remembered, so the next call retries
// rather than permanently wedging the plugin on a transient daemon hiccup.
func (t *dockerTransport) ensureStarted(ctx context.Context, createFn createAndStartFunc) error {
	t.startMu.Lock()
	defer t.startMu.Unlock()
	if t.started {
		return nil
	}
	containerID, addr, err := createFn(ctx)
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.containerID = containerID
	t.addr = addr
	t.mu.Unlock()
	if t.onStarted != nil {
		t.onStarted(t.lifecycleCtx)
	}
	t.started = true
	return nil
}

// buildContainerConfig builds the container + host config for a plugin. Pure
// (no daemon call), so it is unit-tested directly like buildBinds/
// entrypointForMode.
//
// Bridge mode (cfg.HostNetwork false): the shim port is exposed and
// published to an ephemeral 127.0.0.1 host port — unchanged, byte-for-byte,
// from the original inline construction this replaces.
//
// Host mode (cfg.HostNetwork true): NetworkMode "host", no port
// exposing/publishing (host networking shares the daemon host's network
// namespace directly, so there is nothing to publish), and the shim is told
// to bind 127.0.0.1:<port> — LOOPBACK ONLY, never the host's routable
// interfaces — via Cmd, which the shim's ENTRYPOINT appends as flags.
func buildContainerConfig(cfg dockerTransportConfig, shimHostPath string, port int) (*containertypes.Config, *containertypes.HostConfig) {
	cc := &containertypes.Config{
		Image:      cfg.Image,
		Entrypoint: entrypointForMode(cfg.InitMode, cfg.InitPath),
		Env:        envSlice(cfg.Env),
	}
	if cfg.KeepWarm {
		cc.Labels = warmLabels(cfg.PluginID, warmDigest(cfg), apiv1.APIVersion)
	}
	hc := &containertypes.HostConfig{Binds: buildBinds(shimHostPath, cfg.Volumes)}
	if cfg.HostNetwork {
		hc.NetworkMode = "host"
		cc.Cmd = []string{"-addr", "127.0.0.1:" + strconv.Itoa(port)}
		return cc, hc
	}
	cc.ExposedPorts = networktypes.PortSet{pluginInitPort: struct{}{}}
	hc.PortBindings = networktypes.PortMap{
		pluginInitPort: {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: ""}},
	}
	return cc, hc
}

// createAndStart pulls (if needed), creates, starts a plugin container and
// waits for honey-plugin-init inside it to become reachable. Shared by
// dockerTransport's first-use start (CallRaw) and docker_restart.go's recreate
// loop (via createContainer, its createAndStartFunc seam). shimHostPath is the
// daemon-host path of the honey-plugin-init binary to bind-mount (from the
// backend); httpClient is the shim HTTP client (its transport dials through
// the same backend, so readiness polling works against a remote daemon too).
// port is the pre-allocated loopback port for cfg.HostNetwork (0, unused,
// otherwise) — see createContainer, which type-asserts the backend to
// freeLoopbackPortAllocator and allocates it before calling in here.
func createAndStart(ctx context.Context, cli *client.Client, httpClient *http.Client, shimHostPath string, port int, cfg dockerTransportConfig) (containerID, addr string, err error) {
	if cfg.PullPolicy == "always" {
		if pullErr := pullImage(ctx, cli, cfg.Image); pullErr != nil {
			return "", "", pullErr
		}
	}

	// Warm-pool: attach to an already-running compatible container instead of
	// creating a new one. A stale/incompatible match (api_version mismatch) is
	// removed so the create below replaces it.
	var name string
	if cfg.KeepWarm {
		digest := warmDigest(cfg)
		name = warmContainerName(cfg.PluginID, digest)
		id, warmAddr, reusable, staleID := findWarmContainer(ctx, cli, httpClient, digest)
		if reusable {
			zap.L().Debug("plugins: reusing warm container",
				zap.String("plugin_id", cfg.PluginID), zap.String("container_id", id), zap.String("addr", warmAddr))
			return id, warmAddr, nil
		}
		if staleID != "" {
			if rmErr := forceRemoveContainer(ctx, cli, staleID); rmErr != nil {
				zap.L().Warn("plugins: failed to remove incompatible warm container",
					zap.String("container_id", staleID), zap.Error(rmErr))
			}
		}
	}

	if cfg.HostNetwork {
		zap.L().Warn("plugins: starting docker.network: host container — this grants the container the daemon host's full network namespace",
			zap.String("image", cfg.Image), zap.Int("port", port))
	}

	containerCfg, hostCfg := buildContainerConfig(cfg, shimHostPath, port)
	createOpts := client.ContainerCreateOptions{Config: containerCfg, HostConfig: hostCfg, Name: name}

	resp, createErr := cli.ContainerCreate(ctx, createOpts)
	if createErr != nil && strings.Contains(createErr.Error(), "No such image") {
		if pullErr := pullImage(ctx, cli, cfg.Image); pullErr != nil {
			return "", "", fmt.Errorf("plugins: auto-pull %q: %w", cfg.Image, pullErr)
		}
		resp, createErr = cli.ContainerCreate(ctx, createOpts)
	}
	if createErr != nil && name != "" && isWarmNameConflict(createErr) {
		// A leftover (stopped or unreachable) container holds the deterministic
		// warm name — force-remove it and retry once so keep_warm self-heals.
		zap.L().Info("plugins: replacing conflicting warm container", zap.String("name", name))
		if rmErr := forceRemoveContainer(ctx, cli, name); rmErr != nil {
			return "", "", fmt.Errorf("plugins: remove conflicting warm container %q: %w", name, rmErr)
		}
		resp, createErr = cli.ContainerCreate(ctx, createOpts)
	}
	if createErr != nil {
		return "", "", fmt.Errorf("plugins: create container for %q: %w", cfg.Image, createErr)
	}

	if _, startErr := cli.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); startErr != nil {
		wrapped := fmt.Errorf("plugins: start container %q: %w", resp.ID, startErr)
		return "", "", cleanupFailedContainer(ctx, cli, resp.ID, wrapped)
	}

	if cfg.HostNetwork {
		addr, err = waitForReadyHost(ctx, httpClient, port)
	} else {
		addr, err = waitForReady(ctx, cli, httpClient, resp.ID)
	}
	if err != nil {
		return "", "", cleanupFailedContainer(ctx, cli, resp.ID, err)
	}
	return resp.ID, addr, nil
}

// containerRemover is the minimal subset of *client.Client that
// cleanupFailedContainer and stopAndRemoveContainer need. *client.Client
// satisfies this structurally, so production code passes it directly while
// tests can fake just this one method without a real Docker daemon.
type containerRemover interface {
	ContainerRemove(ctx context.Context, containerID string, options client.ContainerRemoveOptions) (client.ContainerRemoveResult, error)
}

// containerStopper is the minimal subset of *client.Client that
// stopAndRemoveContainer needs for its ContainerStop call.
type containerStopper interface {
	ContainerStop(ctx context.Context, containerID string, options client.ContainerStopOptions) (client.ContainerStopResult, error)
}

// cleanupFailedContainer removes a container that was just created but never
// became usable (ContainerStart failed, or waitForReady timed out). Without
// this, docker_restart.go's crash-restart loop — which retries a broken
// plugin forever — leaks one container per failed attempt, unboundedly.
// Removal is best-effort: a failure here is only logged, never returned, so
// the caller always sees the ORIGINAL cause (why the container never came
// up), not a removal failure that would mask it.
func cleanupFailedContainer(ctx context.Context, cli containerRemover, containerID string, cause error) error {
	if _, err := cli.ContainerRemove(ctx, containerID, client.ContainerRemoveOptions{RemoveVolumes: true, Force: true}); err != nil {
		zap.L().Warn("plugins: failed to remove container after failed start/ready",
			zap.String("container_id", containerID), zap.Error(err))
	}
	return cause
}

// stopAndRemoveContainer stops then removes containerID, always attempting
// removal even when ContainerStop fails or times out — ContainerRemove
// already passes Force: true, so it can reap a container regardless of
// whether it stopped cleanly. Skipping removal just because stop errored is
// exactly what let Close leak containers before this fix. Both failures are
// aggregated (via errors.Join, which yields nil when both are nil) so
// neither is silently dropped.
func stopAndRemoveContainer(ctx context.Context, stopper containerStopper, remover containerRemover, containerID string) error {
	_, stopErr := stopper.ContainerStop(ctx, containerID, client.ContainerStopOptions{})
	_, removeErr := remover.ContainerRemove(ctx, containerID, client.ContainerRemoveOptions{RemoveVolumes: true, Force: true})
	return errors.Join(stopErr, removeErr)
}

// buildBinds returns the container bind mounts. In bind mode it prepends the
// read-only honey-plugin-init shim mount; an empty pluginInitHostPath (embedded
// mode) yields no shim bind at all. docker.volumes follow in both modes. Pure
// and unit-tested directly, no Docker daemon needed to verify this construction.
func buildBinds(pluginInitHostPath string, volumes []string) []string {
	binds := make([]string, 0, len(volumes)+1)
	if pluginInitHostPath != "" {
		binds = append(binds, pluginInitHostPath+":"+pluginInitBindPath+":ro")
	}
	binds = append(binds, volumes...)
	return binds
}

// entrypointForMode picks the container entrypoint: the bind-mounted shim path
// in bind mode, or the image's own embedded init path in embedded mode.
func entrypointForMode(mode, initPath string) []string {
	if mode == "embedded" {
		return []string{initPath}
	}
	return []string{pluginInitBindPath}
}

// envSlice renders a name->value map as "NAME=value" entries for
// container.Config.Env, in sorted order for deterministic container creation.
func envSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	names := make([]string, 0, len(env))
	for k := range env {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]string, 0, len(names))
	for _, k := range names {
		out = append(out, k+"="+env[k])
	}
	return out
}

func pullImage(ctx context.Context, cli *client.Client, image string) error {
	rc, err := cli.ImagePull(ctx, image, client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("plugins: pull %q: %w", image, err)
	}
	defer rc.Close()
	_, err = io.Copy(io.Discard, rc)
	return err
}

// waitForReady polls the container's published host port (see pluginInitPort)
// and dials honey-plugin-init over loopback until it accepts a connection or
// the deadline elapses. The retry-loop logic itself is in pollUntilReady
// (Docker-free, unit-tested directly); this function is just the
// Docker-specific "how do I check" glue.
func waitForReady(ctx context.Context, cli *client.Client, httpClient *http.Client, containerID string) (string, error) {
	var addr string
	checkFn := func() (bool, error) {
		inspect, err := cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
		if err != nil || inspect.Container.NetworkSettings == nil {
			return false, nil
		}
		for _, binding := range inspect.Container.NetworkSettings.Ports[pluginInitPort] {
			candidate := fmt.Sprintf("http://127.0.0.1:%s", binding.HostPort)
			ready, fatal := checkHealth(ctx, httpClient, candidate)
			if fatal != nil {
				return false, fatal
			}
			if ready {
				addr = candidate
				return true, nil
			}
		}
		return false, nil
	}
	if err := pollUntilReady(ctx, time.Now().Add(60*time.Second), checkFn); err != nil {
		return "", fmt.Errorf("plugins: container %s: %w", containerID, err)
	}
	return addr, nil
}

// waitForReadyHost polls the shim directly on its known loopback address (host
// networking publishes no ports, so there is nothing to inspect — unlike
// waitForReady, which discovers the published host port via
// ContainerInspect). Reuses the same checkHealth (api_version handshake) +
// pollUntilReady retry loop as the bridge path.
func waitForReadyHost(ctx context.Context, httpClient *http.Client, port int) (string, error) {
	addr := "http://127.0.0.1:" + strconv.Itoa(port)
	checkFn := func() (bool, error) { return checkHealth(ctx, httpClient, addr) }
	if err := pollUntilReady(ctx, time.Now().Add(60*time.Second), checkFn); err != nil {
		return "", fmt.Errorf("plugins: host-network shim on %s: %w", addr, err)
	}
	return addr, nil
}

// pollUntilReady calls checkFn every 200ms until it reports ready, returns a
// fatal error, the deadline passes, or ctx is done. A non-nil fatal error
// (e.g. an api_version mismatch from checkHealth) aborts the loop
// immediately instead of being retried — that failure will never resolve
// itself by waiting longer. Pure retry-loop logic with no Docker dependency,
// so its deadline/cancellation/fatal-abort behavior gets fast, direct unit
// tests instead of relying solely on Task 9's real-Docker integration test
// (architecture-review fix: this loop previously had no test coverage of
// its own).
func pollUntilReady(ctx context.Context, deadline time.Time, checkFn func() (bool, error)) error {
	for {
		ready, fatal := checkFn()
		if fatal != nil {
			return fatal
		}
		if ready {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting to become ready")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// checkHealth GETs /healthz and classifies the result three ways:
//
//	(false, nil)   — not reachable / not 200 yet → keep retrying
//	(false, fatal) — reachable but api_version mismatches honey → STOP, hard-fail
//	(true,  nil)   — reachable and api_version matches → ready
func checkHealth(ctx context.Context, httpClient *http.Client, addr string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr+"/healthz", nil)
	if err != nil {
		return false, nil
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return false, nil // not up yet
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, nil
	}
	var body apiv1.HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false, nil // partial/old server mid-boot — retry
	}
	if body.APIVersion != apiv1.APIVersion {
		return false, fmt.Errorf("honey-plugin-init api_version mismatch (honey expects %q, image reports %q); rebuild the image with a matching honey-plugin-init", apiv1.APIVersion, body.APIVersion)
	}
	return true, nil
}

// CallRaw dispatches on export to one of two calling conventions that
// coexist by design:
//
//   - export == "execute_step": the real recipe-engine convention.
//     Manager.ExecuteStep (step.go) always calls m.Call(ctx, pluginID,
//     "execute_step", apiv1.ExecuteStepInput{...}, &out) — "execute_step" is
//     a fixed export name, and the actual CUE action name plus its config
//     live *inside* the marshaled ExecuteStepInput envelope in inBytes, not
//     in export/inBytes directly. callExecuteStep decodes that envelope,
//     evaluates the real action, and reports the result shaped as
//     apiv1.ExecuteStepOutput — including turning a normal nonzero exec exit
//     into ExecuteStepOutput{Success:false, ...} *data* (exit==0, err==nil)
//     rather than a Go error, since "the step ran and failed" is an expected
//     outcome the engine needs to see, not an aborted call.
//   - anything else: the direct-call convention every existing docker-plugin
//     test (and any other direct Manager.Call/plugins.LoadFromDir(...).Call
//     caller) uses — export IS the CUE action name, inBytes IS the raw
//     config map, and the result is shaped by the action's output_format
//     (json passthrough or a {"output":...}-wrapped string), with a nonzero
//     exec exit reported as an apiv1.PluginError. callDirect is byte-for-byte
//     what CallRaw used to do before execute_step-envelope support existed.
//
// Both branches share execAction (evalAction against plugin.cue + POST to
// honey-plugin-init over HTTP) since that part of the flow is identical.
func (t *dockerTransport) CallRaw(ctx context.Context, export string, inBytes []byte) (int, []byte, error) {
	if err := t.ensureStarted(ctx, t.createContainer); err != nil {
		return 0, nil, fmt.Errorf("plugins: start container on first use: %w", err)
	}
	if export == "execute_step" {
		return t.callExecuteStep(ctx, inBytes)
	}
	return t.callDirect(ctx, export, inBytes)
}

// shimPathForMode returns the daemon-host path of honey-plugin-init to
// bind-mount, or "" in embedded mode (the image supplies its own init, so no
// shim is staged or bound). Keeping the mode gate here — rather than in each
// backend — makes embedded mode skip shim staging uniformly for local and
// remote backends alike: a remote backend's ShimHostPath stages the binary
// onto the target host as a side effect, and embedded mode needs to skip that
// staging entirely, not just discard its result.
func shimPathForMode(ctx context.Context, backend DockerBackend, mode string) (string, error) {
	if mode == "embedded" {
		return "", nil
	}
	return backend.ShimHostPath(ctx)
}

// createContainer resolves the shim binary's daemon-host path from the backend
// (staging it onto a remote host if needed) and pulls/creates/starts the
// plugin container. Used both for first-use start (CallRaw) and crash-recreate
// (docker_restart.go).
func (t *dockerTransport) createContainer(ctx context.Context) (containerID, addr string, err error) {
	shimPath, err := shimPathForMode(ctx, t.backend, t.createCfg.InitMode)
	if err != nil {
		return "", "", fmt.Errorf("plugins: resolve honey-plugin-init: %w", err)
	}
	port := 0
	if t.createCfg.HostNetwork {
		alloc, ok := t.backend.(freeLoopbackPortAllocator)
		if !ok {
			return "", "", fmt.Errorf("plugins: docker.network: host for %q: backend does not support host-network port allocation", t.createCfg.Image)
		}
		if port, err = alloc.FreeLoopbackPort(ctx); err != nil {
			return "", "", fmt.Errorf("plugins: allocate host-network port for %q: %w", t.createCfg.Image, err)
		}
	}
	return createAndStart(ctx, t.backend.Client(), t.httpClient, shimPath, port, t.createCfg)
}

// decodeConfigJSON unmarshals a plugin config object, typing integral numbers as
// int64 (not float64) so they unify with CUE `int` #Config fields in evalAction.
//
// evalAction feeds the map to CUE via ctx.Encode: a Go float64(5) becomes the CUE
// float 5.0 and fails an `int` disjunction with "empty disjunction". A plain
// json.Unmarshal makes every JSON number a float64, so `vus: 5` in a recipe would
// break. UseNumber preserves the original literal, letting us split integral
// literals (→ int64 → CUE int) from fractional ones (→ float64 → CUE float),
// mirroring how CUE's own JSON decoder types numbers. Always returns a non-nil map.
func decodeConfigJSON(raw []byte) (map[string]any, error) {
	config := map[string]any{}
	if len(raw) == 0 {
		return config, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&config); err != nil {
		return nil, err
	}
	return normalizeJSONNumbers(config).(map[string]any), nil
}

// normalizeJSONNumbers walks a decoded-with-UseNumber value and replaces each
// json.Number with an int64 (integral literal) or float64 (fractional), recursing
// through nested objects and arrays. See decodeConfigJSON for why.
func normalizeJSONNumbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		s := x.String()
		if !strings.ContainsAny(s, ".eE") {
			if i, err := x.Int64(); err == nil {
				return i
			}
		}
		if f, err := x.Float64(); err == nil {
			return f
		}
		return s
	case map[string]any:
		for k, val := range x {
			x[k] = normalizeJSONNumbers(val)
		}
		return x
	case []any:
		for i, val := range x {
			x[i] = normalizeJSONNumbers(val)
		}
		return x
	default:
		return v
	}
}

// callDirect is the direct-call convention: export is the CUE action name,
// inBytes is the raw config object. Unchanged from CallRaw's original
// (pre-execute_step-envelope) behavior — every existing docker-plugin test
// exercises this path and must keep passing unmodified.
func (t *dockerTransport) callDirect(ctx context.Context, export string, inBytes []byte) (int, []byte, error) {
	config, err := decodeConfigJSON(inBytes)
	if err != nil {
		return 0, nil, fmt.Errorf("plugins: decode call input: %w", err)
	}
	if t.isRestarting() {
		return 0, nil, fmt.Errorf("plugins: container restarting, retry")
	}

	action, err := t.cue.evalAction(export, config)
	if err != nil {
		return 0, nil, err
	}

	callResp, err := t.execAction(ctx, export, action)
	if err != nil {
		return 0, nil, err
	}

	if callResp.Error != "" {
		envelope, _ := json.Marshal(apiv1.PluginError{Error: callResp.Error})
		return 1, envelope, nil
	}
	if callResp.ExitCode != 0 {
		envelope, _ := json.Marshal(apiv1.PluginError{Error: nonZeroExitMessage(callResp)})
		return callResp.ExitCode, envelope, nil
	}

	if action.OutputFormat == "json" {
		if !json.Valid([]byte(callResp.Output)) {
			return 0, nil, fmt.Errorf("plugins: action %q: output_format json but output isn't valid JSON: %s", export, callResp.Output)
		}
		return 0, []byte(callResp.Output), nil
	}
	envelope, err := json.Marshal(map[string]string{"output": callResp.Output})
	if err != nil {
		return 0, nil, err
	}
	return 0, envelope, nil
}

// callExecuteStep is the recipe-engine convention: inBytes is a marshaled
// apiv1.ExecuteStepInput envelope. The real action name and config live
// inside it (stepIn.Action / stepIn.Config), not in the "execute_step"
// export string itself. Reports success/failure/shim-error as
// apiv1.ExecuteStepOutput or apiv1.PluginError per CallRaw's doc comment.
func (t *dockerTransport) callExecuteStep(ctx context.Context, inBytes []byte) (int, []byte, error) {
	var stepIn apiv1.ExecuteStepInput
	if len(inBytes) > 0 {
		if err := json.Unmarshal(inBytes, &stepIn); err != nil {
			return 0, nil, fmt.Errorf("plugins: decode execute_step input: %w", err)
		}
	}
	config, err := decodeConfigJSON(stepIn.Config)
	if err != nil {
		return 0, nil, fmt.Errorf("plugins: decode execute_step config: %w", err)
	}

	if t.isRestarting() {
		return 0, nil, fmt.Errorf("plugins: container restarting, retry")
	}

	action, err := t.cue.evalAction(stepIn.Action, config)
	if err != nil {
		return 0, nil, err
	}

	callResp, err := t.execAction(ctx, stepIn.Action, action)
	if err != nil {
		return 0, nil, err
	}

	// Shim-level failure (honey-plugin-init couldn't even exec the binary):
	// genuinely infrastructure-level, unchanged from the direct-call
	// convention — still becomes a Go error once Manager.Call sees the
	// nonzero exit / decodable PluginError below.
	if callResp.Error != "" {
		envelope, _ := json.Marshal(apiv1.PluginError{Error: callResp.Error})
		return 1, envelope, nil
	}

	// The exec'd program ran and exited nonzero: an expected, normal step
	// failure. This must flow through as ExecuteStepOutput *data*
	// (exit==0, err==nil) so Manager.Call decodes it cleanly into the
	// caller's *apiv1.ExecuteStepOutput instead of returning a Go error for
	// what may just be a plugin action legitimately failing on bad input.
	if callResp.ExitCode != 0 {
		out := apiv1.ExecuteStepOutput{
			Success:  false,
			ExitCode: callResp.ExitCode,
			Stdout:   callResp.Output,
			Stderr:   callResp.Stderr,
			Err:      nonZeroExitMessage(callResp),
		}
		envelope, err := json.Marshal(out)
		if err != nil {
			return 0, nil, err
		}
		return 0, envelope, nil
	}

	// Success. output_format's only remaining job here is validating that a
	// declared "json" action actually produced valid JSON — Stdout is always
	// the plain string content of callResp.Output regardless of
	// output_format, since ExecuteStepOutput.Stdout is a plain string field
	// and can't have two wire shapes the way the direct-call convention's
	// bare `out []byte` can. A declared-json action producing invalid JSON is
	// a plugin-authoring bug, not step data, so — consistent with how the
	// direct-call convention (callDirect, above) already reports this exact
	// same validation failure — it's surfaced as a Go error from CallRaw
	// itself rather than folded into ExecuteStepOutput.Err.
	if action.OutputFormat == "json" && !json.Valid([]byte(callResp.Output)) {
		return 0, nil, fmt.Errorf("plugins: action %q: output_format json but output isn't valid JSON: %s", stepIn.Action, callResp.Output)
	}

	out := apiv1.ExecuteStepOutput{
		Success:  true,
		ExitCode: 0,
		Stdout:   callResp.Output,
		Stderr:   callResp.Stderr,
	}
	envelope, err := json.Marshal(out)
	if err != nil {
		return 0, nil, err
	}
	return 0, envelope, nil
}

// execAction evaluates action's plugin.cue-derived argv/env/stdin, execs it
// inside the container via honey-plugin-init, and returns the raw
// apiv1.ExecResponse. Shared by callDirect and callExecuteStep, which each
// interpret the response differently for their own calling convention.
// exportForErr is used only to label the "call %s" wrapped error below.
func (t *dockerTransport) execAction(ctx context.Context, exportForErr string, action actionResult) (apiv1.ExecResponse, error) {
	reqBody, err := json.Marshal(apiv1.ExecRequest{Argv: action.Argv, Env: action.Env, Stdin: []byte(action.Stdin)})
	if err != nil {
		return apiv1.ExecResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.currentAddr()+"/call", bytes.NewReader(reqBody))
	if err != nil {
		return apiv1.ExecResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return apiv1.ExecResponse{}, fmt.Errorf("plugins: call %s: %w", exportForErr, err)
	}
	defer httpResp.Body.Close()

	var callResp apiv1.ExecResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&callResp); err != nil {
		return apiv1.ExecResponse{}, fmt.Errorf("plugins: decode call response: %w", err)
	}
	return callResp, nil
}

// nonZeroExitMessage picks the best available description of a nonzero exec
// exit: the process's own stderr when it wrote any, else a generic fallback.
func nonZeroExitMessage(resp apiv1.ExecResponse) string {
	if resp.Stderr != "" {
		return resp.Stderr
	}
	return fmt.Sprintf("exited with code %d", resp.ExitCode)
}

// Close stops the crash-watcher (so a deliberate shutdown doesn't trigger a
// spurious restart), then stops and removes the container — removal is
// attempted even if stopping fails or times out (see stopAndRemoveContainer),
// so a stop failure alone can no longer leak the container. It also cancels
// the transport's internally-derived context so that any restart backoff
// currently in progress (docker_restart.go) is interrupted immediately —
// closing stopWatch alone only unblocks watchLoop's idle select, not a
// backoff.Retry call already underway inside restart.
func (t *dockerTransport) Close(ctx context.Context) error {
	t.startMu.Lock()
	started := t.started
	t.startMu.Unlock()

	t.mu.Lock()
	if t.stopWatch != nil {
		close(t.stopWatch)
	}
	id := t.containerID
	t.mu.Unlock()

	if t.cancel != nil {
		t.cancel()
	}

	cli := t.backend.Client()
	defer t.backend.Close()
	if !started {
		// Never called: no container was ever created, so there's nothing
		// to stop/remove.
		return nil
	}
	if t.createCfg.KeepWarm {
		// Warm-pool: leave the container running so the next run reuses it.
		// Closing the moby client (deferred above) does not stop the
		// container — it runs independently until `honey plugins gc`.
		zap.L().Debug("plugins: leaving warm container running (keep_warm)",
			zap.String("plugin_id", t.createCfg.PluginID), zap.String("container_id", id))
		return nil
	}
	return stopAndRemoveContainer(ctx, cli, cli, id)
}
