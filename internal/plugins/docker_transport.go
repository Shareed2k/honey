package plugins

import (
	"bytes"
	"context"
	"encoding/json"
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
		return "", "", fmt.Errorf("plugins: start container %q: %w", resp.ID, startErr)
	}

	addr, err = waitForReady(ctx, cli, resp.ID)
	if err != nil {
		return "", "", err
	}
	return resp.ID, addr, nil
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

// CallRaw evaluates the action's plugin.cue argv/output_format against the
// call's config, execs it inside the container via honey-plugin-init, and
// translates the result into Manager.Call's expected envelope: exit==0 with
// outBytes decodable as the caller's `out` type on success, or exit!=0 with
// outBytes decodable as apiv1.PluginError on failure.
func (t *dockerTransport) CallRaw(ctx context.Context, export string, inBytes []byte) (int, []byte, error) {
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

	reqBody, err := json.Marshal(apiv1.ExecRequest{Argv: action.Argv, Env: action.Env, Stdin: []byte(action.Stdin)})
	if err != nil {
		return 0, nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.currentAddr()+"/call", bytes.NewReader(reqBody))
	if err != nil {
		return 0, nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return 0, nil, fmt.Errorf("plugins: call %s: %w", export, err)
	}
	defer httpResp.Body.Close()

	var callResp apiv1.ExecResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&callResp); err != nil {
		return 0, nil, fmt.Errorf("plugins: decode call response: %w", err)
	}

	if callResp.Error != "" {
		envelope, _ := json.Marshal(apiv1.PluginError{Error: callResp.Error})
		return 1, envelope, nil
	}
	if callResp.ExitCode != 0 {
		msg := callResp.Stderr
		if msg == "" {
			msg = fmt.Sprintf("exited with code %d", callResp.ExitCode)
		}
		envelope, _ := json.Marshal(apiv1.PluginError{Error: msg})
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

// Close stops the crash-watcher (so a deliberate shutdown doesn't trigger a
// spurious restart), then stops and removes the container. It also cancels
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
	if _, err := t.cli.ContainerStop(ctx, id, client.ContainerStopOptions{}); err != nil {
		return err
	}
	_, err := t.cli.ContainerRemove(ctx, id, client.ContainerRemoveOptions{RemoveVolumes: true, Force: true})
	return err
}
