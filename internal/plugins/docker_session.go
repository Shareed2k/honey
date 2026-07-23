package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
	"github.com/shareed2k/honey/internal/stepkv"
)

// DockerHostSession scopes remote docker-plugin containers to one recipe run:
// one long-lived shim-container per (pluginID, hostKey), reused across the
// session, all stopped+removed on Close. The engine creates one per run so
// containers never outlive a run — important for the long-lived `honey web`
// process, where a single Manager is shared across scheduled runs and a
// leaked container would sit on the target host indefinitely.
//
// It reuses the exact same dockerTransport (shim protocol, /call HTTP,
// container lifecycle) as the local path; only the DockerBackend differs
// (see the engine's ssh backend), so remote and local execution share one
// code path.
type DockerHostSession struct {
	m          *Manager
	mu         sync.Mutex
	transports map[string]*sessionTransport
	closed     bool
}

// sessionTransport pairs a per-host transport with a call mutex —
// dockerTransport is not safe for concurrent calls, and graph-mode recipes can
// run two plugin steps against the same host concurrently.
type sessionTransport struct {
	tr     *dockerTransport
	callMu sync.Mutex
}

// NewDockerHostSession returns a session bound to m's loaded plugins.
func (m *Manager) NewDockerHostSession() *DockerHostSession {
	return &DockerHostSession{m: m, transports: make(map[string]*sessionTransport)}
}

// ExecuteStep runs the execute_step export for pluginID on the daemon that the
// backend built by newBackend targets, against a shim-container keyed by
// (pluginID, hostKey) and reused for the session's lifetime. newBackend is
// invoked only on a cache miss, so a caller building an expensive backend (an
// SSH-tunneled moby client) pays that cost once per host, not per call.
//
// The result decoding mirrors Manager.Call exactly: a plugin-reported error or
// nonzero transport exit becomes a Go error; a normal step failure arrives as
// ExecuteStepOutput data (Success:false) via the transport's own convention.
func (s *DockerHostSession) ExecuteStep(ctx context.Context, newBackend func() (DockerBackend, error), hostKey, pluginID, action string, config json.RawMessage, stepIndex int, hostJSON []byte, env map[string]string, execute, secretsDry bool, kvSession *stepkv.Session) (apiv1.ExecuteStepOutput, error) {
	var out apiv1.ExecuteStepOutput
	if s == nil || s.m == nil || !s.m.enabled {
		return out, fmt.Errorf("plugins: disabled")
	}
	pluginID = strings.TrimSpace(pluginID)
	s.m.mu.Lock()
	lp, ok := s.m.byID[pluginID]
	s.m.mu.Unlock()
	if !ok || lp == nil {
		return out, fmt.Errorf("plugins: unknown plugin %q", pluginID)
	}
	if lp.manifest.effectiveRuntime() != "docker" {
		return out, fmt.Errorf("plugins: %q is not a runtime:docker plugin", pluginID)
	}

	entry, err := s.transportFor(ctx, newBackend, hostKey, pluginID, lp)
	if err != nil {
		return out, err
	}

	in := apiv1.ExecuteStepInput{
		APIVersion: apiv1.APIVersion,
		StepIndex:  stepIndex,
		Host:       hostJSON,
		Env:        env,
		PluginID:   pluginID,
		Action:     action,
		Config:     config,
		Execute:    execute,
		SecretsDry: secretsDry,
	}
	inBytes, err := json.Marshal(in)
	if err != nil {
		return out, err
	}

	callCtx := ctx
	if kvSession != nil {
		callCtx = WithKVSession(ctx, kvSession)
	}
	if _, hasDeadline := callCtx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(callCtx, time.Duration(s.m.TimeoutMS())*time.Millisecond)
		defer cancel()
	}

	entry.callMu.Lock()
	exit, outBytes, callErr := entry.tr.CallRaw(callCtx, "execute_step", inBytes)
	entry.callMu.Unlock()
	if callErr != nil {
		return out, fmt.Errorf("plugins: %s.execute_step: %w", pluginID, callErr)
	}
	var pe apiv1.PluginError
	if jsonErr := json.Unmarshal(outBytes, &pe); jsonErr == nil && strings.TrimSpace(pe.Error) != "" {
		return out, fmt.Errorf("plugins: %s.execute_step: %s", pluginID, pe.Error)
	}
	if exit != 0 {
		return out, fmt.Errorf("plugins: %s.execute_step: plugin returned exit code %d", pluginID, exit)
	}
	if err := json.Unmarshal(outBytes, &out); err != nil {
		return out, fmt.Errorf("plugins: %s.execute_step: decode output: %w", pluginID, err)
	}
	return out, nil
}

func (s *DockerHostSession) transportFor(ctx context.Context, newBackend func() (DockerBackend, error), hostKey, pluginID string, lp *loadedPlugin) (*sessionTransport, error) {
	key := pluginID + "\x00" + hostKey
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("plugins: docker host session closed")
	}
	if entry, ok := s.transports[key]; ok {
		return entry, nil
	}
	backend, err := newBackend()
	if err != nil {
		return nil, err
	}
	cfg, err := dockerConfigForRemote(lp)
	if err != nil {
		backend.Close()
		return nil, err
	}
	tr, err := newDockerTransport(ctx, backend, cfg)
	if err != nil {
		return nil, err // newDockerTransport closed backend on error
	}
	entry := &sessionTransport{tr: tr}
	s.transports[key] = entry
	return entry, nil
}

// Close stops and removes every per-host container the session created,
// returning the first teardown error (all are attempted).
func (s *DockerHostSession) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	entries := s.transports
	s.transports = make(map[string]*sessionTransport)
	s.closed = true
	s.mu.Unlock()
	var first error
	for _, entry := range entries {
		if err := entry.tr.Close(ctx); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// dockerConfigForRemote builds the transport config for a remote docker plugin
// from its manifest. Unlike the local loader (loadDockerPluginDir) it does NOT
// append the operator's ~/.honey/workspaces mount — that path is operator-local
// and meaningless on a remote daemon; only the manifest's declared
// docker.volumes are mounted (they resolve on the remote host, which is what
// makes e.g. /var/run/docker.sock refer to the remote daemon's socket).
func dockerConfigForRemote(lp *loadedPlugin) (dockerTransportConfig, error) {
	maxBackoff, err := lp.manifest.Docker.effectiveMaxBackoff()
	if err != nil {
		return dockerTransportConfig{}, fmt.Errorf("plugins: invalid docker.restart.max_backoff: %w", err)
	}
	return dockerTransportConfig{
		Image:      lp.manifest.Docker.Image,
		PullPolicy: lp.manifest.Docker.effectivePullPolicy(),
		CueSource:  lp.cueSource,
		MaxBackoff: maxBackoff,
		Env:        resolveAllowedEnv(lp.manifest.AllowedEnv),
		Volumes:    append([]string(nil), lp.manifest.Docker.Volumes...),
		InitMode:   lp.manifest.Docker.effectiveInitMode(),
		InitPath:   lp.manifest.Docker.InitPath,
	}, nil
}
