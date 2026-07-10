package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"sort"
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

// dockerTransport runs a plugin as a long-lived Docker container (for the
// Manager's process lifetime — not one container per call), reached over
// HTTP via the honey-plugin-init binary bind-mounted as its entrypoint.
type dockerTransport struct {
	cli        *client.Client
	cue        *pluginCue
	httpClient *http.Client
	createCfg  dockerTransportConfig //nolint:unused // read by Task 6's restart logic (not yet wired into Manager on this branch)

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
//
//nolint:unused // constructed by Task 7's Manager wiring, not yet landed on this branch
type dockerTransportConfig struct {
	Image              string
	PullPolicy         string // "if_not_present" or "always"
	PluginInitHostPath string // path to the honey-plugin-init binary on the honey host
	CueSource          []byte
	MaxBackoff         time.Duration
	Env                map[string]string // resolved allowed_env values, passed through as container env vars
	Volumes            []string          // static bind mounts from manifest.Docker.Volumes, Docker bind syntax
}

//nolint:unused // called by Task 7's Manager wiring, not yet landed on this branch
func newDockerTransport(ctx context.Context, cfg dockerTransportConfig) (*dockerTransport, error) {
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("plugins: docker client: %w", err)
	}
	pc, err := newPluginCue(cfg.CueSource)
	if err != nil {
		cli.Close()
		return nil, err
	}

	containerID, addr, err := createAndStart(ctx, cli, cfg)
	if err != nil {
		cli.Close()
		return nil, err
	}

	// internalCtx is derived from the caller's ctx but owned by the
	// transport itself: Close cancels it directly (see Close below) so the
	// watch/restart lifecycle can always be interrupted, regardless of
	// whether the caller's own ctx ever gets cancelled.
	internalCtx, cancel := context.WithCancel(ctx)
	dt := &dockerTransport{
		cli:         cli,
		cue:         pc,
		httpClient:  &http.Client{},
		createCfg:   cfg,
		containerID: containerID,
		addr:        addr,
		cancel:      cancel,
	}
	dt.startWatching(internalCtx)
	return dt, nil
}

// createAndStart pulls (if needed), creates, starts a plugin container and
// waits for honey-plugin-init inside it to become reachable. Shared by
// newDockerTransport (initial start) and docker_restart.go's recreate loop.
//
//nolint:unused // called by newDockerTransport and reused by Task 6's restart logic, not yet landed on this branch
func createAndStart(ctx context.Context, cli *client.Client, cfg dockerTransportConfig) (containerID, addr string, err error) {
	if cfg.PullPolicy == "always" {
		if pullErr := pullImage(ctx, cli, cfg.Image); pullErr != nil {
			return "", "", pullErr
		}
	}

	containerCfg := &containertypes.Config{
		Image:        cfg.Image,
		Entrypoint:   []string{pluginInitBindPath},
		Env:          envSlice(cfg.Env),
		ExposedPorts: networktypes.PortSet{pluginInitPort: struct{}{}},
	}
	hostCfg := &containertypes.HostConfig{
		Binds: buildBinds(cfg.PluginInitHostPath, cfg.Volumes),
		PortBindings: networktypes.PortMap{
			pluginInitPort: {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: ""}},
		},
	}
	createOpts := client.ContainerCreateOptions{Config: containerCfg, HostConfig: hostCfg}

	resp, createErr := cli.ContainerCreate(ctx, createOpts)
	if createErr != nil && strings.Contains(createErr.Error(), "No such image") {
		if pullErr := pullImage(ctx, cli, cfg.Image); pullErr != nil {
			return "", "", fmt.Errorf("plugins: auto-pull %q: %w", cfg.Image, pullErr)
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

	addr, err = waitForReady(ctx, cli, resp.ID)
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

// buildBinds prepends the mandatory honey-plugin-init bind mount (read-only)
// to the manifest's static docker.volumes entries, in that order — pure and
// unit-tested directly, no Docker daemon needed to verify this construction.
func buildBinds(pluginInitHostPath string, volumes []string) []string {
	binds := make([]string, 0, len(volumes)+1)
	binds = append(binds, pluginInitHostPath+":"+pluginInitBindPath+":ro")
	binds = append(binds, volumes...)
	return binds
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

//nolint:unused // called by createAndStart, which is wired in by Task 7 (not yet landed on this branch)
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
//
//nolint:unused // called by createAndStart, which is wired in by Task 7 (not yet landed on this branch)
func waitForReady(ctx context.Context, cli *client.Client, containerID string) (string, error) {
	var addr string
	checkFn := func() bool {
		inspect, err := cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
		if err != nil || inspect.Container.NetworkSettings == nil {
			return false
		}
		for _, binding := range inspect.Container.NetworkSettings.Ports[pluginInitPort] {
			candidate := fmt.Sprintf("http://127.0.0.1:%s", binding.HostPort)
			if pingReady(ctx, candidate) {
				addr = candidate
				return true
			}
		}
		return false
	}
	if err := pollUntilReady(ctx, time.Now().Add(60*time.Second), checkFn); err != nil {
		return "", fmt.Errorf("plugins: container %s: %w", containerID, err)
	}
	return addr, nil
}

// pollUntilReady calls checkFn every 200ms until it returns true, the
// deadline passes, or ctx is done. Pure retry-loop logic with no Docker
// dependency, so its deadline/cancellation behavior gets fast, direct unit
// tests instead of relying solely on Task 9's real-Docker integration test
// (architecture-review fix: this loop previously had no test coverage of
// its own).
func pollUntilReady(ctx context.Context, deadline time.Time, checkFn func() bool) error {
	for {
		if checkFn() {
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

//nolint:unused // called by waitForReady, which is wired in by Task 7 (not yet landed on this branch)
func pingReady(ctx context.Context, addr string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr+"/healthz", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
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
	if export == "execute_step" {
		return t.callExecuteStep(ctx, inBytes)
	}
	return t.callDirect(ctx, export, inBytes)
}

// callDirect is the direct-call convention: export is the CUE action name,
// inBytes is the raw config object. Unchanged from CallRaw's original
// (pre-execute_step-envelope) behavior — every existing docker-plugin test
// exercises this path and must keep passing unmodified.
func (t *dockerTransport) callDirect(ctx context.Context, export string, inBytes []byte) (int, []byte, error) {
	var config map[string]any
	if len(inBytes) > 0 {
		if err := json.Unmarshal(inBytes, &config); err != nil {
			return 0, nil, fmt.Errorf("plugins: decode call input: %w", err)
		}
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
	var config map[string]any
	if len(stepIn.Config) > 0 {
		if err := json.Unmarshal(stepIn.Config, &config); err != nil {
			return 0, nil, fmt.Errorf("plugins: decode execute_step config: %w", err)
		}
	}
	if config == nil {
		config = map[string]any{}
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
	t.mu.Lock()
	if t.stopWatch != nil {
		close(t.stopWatch)
	}
	id := t.containerID
	t.mu.Unlock()

	if t.cancel != nil {
		t.cancel()
	}

	defer t.cli.Close()
	return stopAndRemoveContainer(ctx, t.cli, t.cli, id)
}
