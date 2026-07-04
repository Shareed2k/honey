package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/truenasprovider"
	"github.com/shareed2k/honey/internal/sshclient"
	"go.uber.org/zap"
)

// StreamCueStepTunnel ...
func init() {
	RegisterStepExecutor(cuetry.KindTunnel, &TunnelExecutor{})
}

// TunnelExecutor executes the corresponding recipe step.
type TunnelExecutor struct{}

// ExecuteStream streams the step execution.
func (e *TunnelExecutor) ExecuteStream(ctx context.Context, req ExecutionRequest, opts ExecutionOptions, resCh chan<- HostExecResult) error {
	stepIdx, step, targets, ch, retryCfg, attemptMax := req.Index, req.Step, req.Targets, resCh, req.RetryCfg, req.AttemptMax
	if _, ok := step.(*cuetry.TunnelStep); !ok {
		return fmt.Errorf("internal tunnel step")
	}
	maxConc := clampConcurrency(RecipeHostMaxConc(step, opts.Recipe.Defaults), 8)
	sem := make(chan struct{}, maxConc)
	var wg sync.WaitGroup
	for _, target := range targets {
		target := target
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			outcome := RunHostExecWithRetry(ctx, retryCfg, func() HostExecResult {
				return runCueTunnelOnHost(ctx, step, target.Record, opts.SSHUser, stepIdx, opts.Cache, opts.TunnelCoord, opts.Execute)
			})
			RecordMaxAttempts(attemptMax, outcome.Attempts)
			ch <- outcome.Result
		}()
	}
	wg.Wait()
	return nil
}

func runCueTunnelOnHost(
	ctx context.Context,
	step cuetry.Step,
	target hosts.Record,
	sshUser string,
	stepIdx int,
	cache *ClientCache,
	tunnelCoord *RecipeTunnelCoordinator,
	execute bool,
) HostExecResult {
	res := HostExecResult{Name: target.Name, IP: target.PrimaryIP, Provider: target.Provider, Success: false}
	ts, _ := step.(*cuetry.TunnelStep)
	if ts == nil || ts.Tunnel == nil {
		res.ErrMsg = "internal: tunnel step missing tunnel field"
		return res
	}
	t := ts.Tunnel
	stepID := strings.TrimSpace(step.Base().ID)
	if stepID == "" {
		stepID = fmt.Sprintf("step%d", stepIdx)
	}

	if !execute {
		res.Success = true
		res.Skipped = false
		res.Output = tunnelDryRunJSON(t)
		return res
	}

	mode := cuetry.EffectiveTunnelMode(t)
	remoteHost := strings.TrimSpace(t.RemoteHost)
	if remoteHost == "" {
		remoteHost = "localhost"
	}
	poolKey := tunnelPoolKey(target, sshUser, t)
	zap.L().Debug(
		"recipe tunnel starting",
		zap.String("step_id", stepID),
		zap.String("host_name", target.Name),
		zap.String("provider", target.Provider),
		zap.String("mode", mode),
		zap.String("pool_key", poolKey),
		zap.String("remote_host", remoteHost),
		zap.Int("remote_port", t.RemotePort),
	)
	ep, release, err := tunnelCoord.Acquire(ctx, poolKey, func(cctx context.Context) (TunnelEndpoint, func(), error) {
		return startTunnelForRecord(cctx, sshUser, target, t, cache)
	})
	if err != nil {
		zap.L().Debug(
			"recipe tunnel failed",
			zap.String("step_id", stepID),
			zap.String("host_name", target.Name),
			zap.Error(err),
		)
		res.ErrMsg = err.Error()
		return res
	}
	tunnelCoord.Register(stepID, sshUser, target, ep, release)
	zap.L().Debug(
		"recipe tunnel ready",
		zap.String("step_id", stepID),
		zap.String("host_name", target.Name),
		zap.String("listen_host", ep.Host),
		zap.Int("listen_port", ep.Port),
		zap.String("share_key", ep.ShareKey),
	)
	res.Success = true
	res.Output = tunnelEndpointJSON(ep)
	return res
}

func startTunnelForRecord(ctx context.Context, user string, r hosts.Record, t *cuetry.RecipeStepTunnel, cache *ClientCache) (TunnelEndpoint, func(), error) {
	mode := cuetry.EffectiveTunnelMode(t)
	bind := effectiveTunnelBind(t.Bind)
	localPort := t.LocalPort
	remoteHost := strings.TrimSpace(t.RemoteHost)
	if remoteHost == "" {
		remoteHost = "localhost"
	}
	remotePort := t.RemotePort

	if t.UseSSHConfig {
		alias := r.PrimaryIP
		if n := strings.TrimSpace(r.Name); n != "" {
			alias = n
		}
		fs, err := sshclient.ForwardsForHost(alias, user, t.SSHConfigEnv)
		if err != nil {
			return TunnelEndpoint{}, nil, err
		}
		spec, err := pickSSHConfigForward(fs, t.SSHConfigMatch, mode)
		if err != nil {
			return TunnelEndpoint{}, nil, err
		}
		if spec.BindPort > 0 && localPort == 0 {
			localPort = spec.BindPort
		}
		if strings.TrimSpace(spec.RemoteHost) != "" {
			remoteHost = spec.RemoteHost
		}
		if spec.RemotePort > 0 {
			remotePort = spec.RemotePort
		}
	}

	switch {
	case r.Provider == "k8s":
		zap.L().Debug("recipe tunnel backend", zap.String("backend", "k8s"), zap.String("host_name", r.Name))
		host, port, stop, err := StartK8sPortForward(ctx, r, localPort, remotePort)
		if err != nil {
			return TunnelEndpoint{}, nil, err
		}
		return TunnelEndpoint{Host: host, Port: port, Mode: "local", RemoteHost: "pod", RemotePort: remotePort, ShareKey: t.ShareKey}, stop, nil
	case truenasprovider.TruenasTunnelUsesAPIShell(r):
		zap.L().Debug("recipe tunnel backend", zap.String("backend", "truenas"), zap.String("host_name", r.Name))
		lp := localPort
		if lp == 0 {
			lp = remotePort
		}
		spec := fmt.Sprintf("%d:%s:%d", lp, remoteHost, remotePort)
		host, port, stop, err := StartTrueNASForward(ctx, user, r, spec)
		if err != nil {
			return TunnelEndpoint{}, nil, err
		}
		return TunnelEndpoint{Host: host, Port: port, Mode: mode, RemoteHost: remoteHost, RemotePort: remotePort, ShareKey: t.ShareKey}, stop, nil
	default:
		zap.L().Debug("recipe tunnel backend", zap.String("backend", "ssh"), zap.String("host_name", r.Name))
		return startSSHTunnel(ctx, user, r, t, cache, mode, bind, localPort, remoteHost, remotePort)
	}
}

func startSSHTunnel(ctx context.Context, user string, r hosts.Record, t *cuetry.RecipeStepTunnel, cache *ClientCache, mode, bind string, localPort int, remoteHost string, remotePort int) (TunnelEndpoint, func(), error) {
	client, err := cache.GetOrDial(user, r)
	if err != nil {
		return TunnelEndpoint{}, nil, err
	}
	sshPort := 0
	if p, ok := hosts.MetaSSHPort(&r); ok {
		sshPort = p
	}
	alias := r.PrimaryIP

	switch mode {
	case "remote":
		remoteBind := effectiveTunnelBind(t.RemoteBind)
		if remoteBind == "" {
			remoteBind = "127.0.0.1"
		}
		localHost := strings.TrimSpace(t.LocalHost)
		if localHost == "" {
			localHost = "127.0.0.1"
		}
		remAddr, stop, err := client.StartRemoteForward(ctx, remoteBind, t.RemoteListen, localHost, t.LocalTarget)
		if err != nil {
			return TunnelEndpoint{}, nil, err
		}
		_, remPortStr, _ := netSplitHostPort(remAddr)
		rp, _ := strconv.Atoi(remPortStr)
		return TunnelEndpoint{Host: remoteBind, Port: rp, Mode: mode, ShareKey: t.ShareKey}, stop, nil
	case "dynamic":
		host, port, stop, err := client.StartDynamicForward(ctx, bind, localPort)
		if err != nil {
			return TunnelEndpoint{}, nil, err
		}
		return TunnelEndpoint{Host: host, Port: port, Mode: mode, ShareKey: t.ShareKey}, stop, nil
	case "udp":
		host, port, stop, err := client.StartUDPRelay(ctx, bind, localPort, remoteHost, remotePort, t.RemoteSocat)
		if err != nil {
			return TunnelEndpoint{}, nil, err
		}
		return TunnelEndpoint{Host: host, Port: port, Mode: mode, RemoteHost: remoteHost, RemotePort: remotePort, ShareKey: t.ShareKey}, stop, nil
	case "tun":
		tunName, stop, err := client.StartTunForward(ctx, user, alias, sshPort, t.TunLocal, t.TunRemote)
		if err != nil {
			return TunnelEndpoint{}, nil, err
		}
		return TunnelEndpoint{Mode: mode, TunName: tunName, ShareKey: t.ShareKey}, stop, nil
	default:
		host, port, stop, err := client.StartLocalForward(ctx, bind, localPort, remoteHost, remotePort)
		if err != nil {
			return TunnelEndpoint{}, nil, err
		}
		return TunnelEndpoint{Host: host, Port: port, Mode: "local", RemoteHost: remoteHost, RemotePort: remotePort, ShareKey: t.ShareKey}, stop, nil
	}
}

func effectiveTunnelBind(bind string) string {
	bind = strings.TrimSpace(bind)
	if bind == "" || bind == "localhost" {
		return "127.0.0.1"
	}
	return bind
}

func tunnelPoolKey(r hosts.Record, user string, t *cuetry.RecipeStepTunnel) string {
	if sk := strings.TrimSpace(t.ShareKey); sk != "" {
		return TunnelLookupKeyForShare(sk, "")
	}
	mode := cuetry.EffectiveTunnelMode(t)
	spec := fmt.Sprintf("%s:%d:%s:%d:%d", mode, t.RemotePort, t.RemoteHost, t.LocalPort, t.RemoteListen)
	derived := TunnelDerivedKey(mode, r.Provider, SSHClientCacheKey(user, r), spec)
	return TunnelLookupKeyForShare("", derived)
}

func tunnelEndpointJSON(ep TunnelEndpoint) string {
	m := map[string]any{
		"host": ep.Host,
		"port": ep.Port,
		"mode": ep.Mode,
	}
	if ep.RemoteHost != "" {
		m["remote_host"] = ep.RemoteHost
	}
	if ep.RemotePort > 0 {
		m["remote_port"] = ep.RemotePort
	}
	if ep.TunName != "" {
		m["tun_name"] = ep.TunName
	}
	if ep.ShareKey != "" {
		m["share_key"] = ep.ShareKey
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func tunnelDryRunJSON(t *cuetry.RecipeStepTunnel) string {
	m := map[string]any{
		"host": "<<127.0.0.1>>",
		"port": "<<port>>",
		"mode": cuetry.EffectiveTunnelMode(t),
	}
	if t.UseSSHConfig {
		m["ssh_config"] = "via ssh -G"
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// ExecuteDryRun executes a dry run of the step.
func (e *TunnelExecutor) ExecuteDryRun(_ context.Context, req ExecutionRequest, opts ExecutionOptions, out io.Writer) error {
	out, recipe, i, step, targets := out, opts.Recipe, req.Index, req.Step, req.Targets
	WriteCueStepNotifyDryLine(out, step)
	WriteCueStepRetryDryLine(out, i, cuetry.EffectiveRetry(step.Base(), recipe.Defaults))
	for _, target := range targets {
		_, _ = fmt.Fprintf(out, "step %d: kind=tunnel name=%q %s mode=%s output=%s\n",
			i, target.Record.Name, FormatTargetForDryRun(target.Record), cuetry.EffectiveTunnelMode(tunnelOf(step)), tunnelDryRunJSON(tunnelOf(step)))
	}
	return nil
}

func netSplitHostPort(addr string) (string, string, error) {
	parts := strings.Split(addr, ":")
	if len(parts) < 2 {
		return addr, "", fmt.Errorf("bad addr")
	}
	return parts[0], parts[len(parts)-1], nil
}

func pickSSHConfigForward(fs sshclient.ForwardSet, match, mode string) (sshclient.ForwardSpec, error) {
	match = strings.TrimSpace(match)
	var specs []sshclient.ForwardSpec
	switch mode {
	case "remote":
		specs = fs.Remote
	case "dynamic":
		specs = fs.Dynamic
	default:
		specs = fs.Local
	}
	if match != "" {
		return sshclient.PickForward(specs, match)
	}
	if len(specs) == 0 {
		return sshclient.ForwardSpec{}, fmt.Errorf("no %s forward in ssh_config", mode)
	}
	return specs[0], nil
}

// StartTrueNASForward starts a non-blocking TrueNAS API shell tunnel.
// StartTrueNASForward ...
func StartTrueNASForward(ctx context.Context, user string, r hosts.Record, localFwd string) (host string, port int, stop func(), err error) {
	localPort, _, _, err := sshclient.ParseLocalForward(localFwd)
	if err != nil {
		return "", 0, nil, err
	}
	lp, _ := strconv.Atoi(localPort)
	runCtx, cancel := context.WithCancel(ctx)
	go func() {
		_ = RunTrueNASTunnel(runCtx, user, r, localFwd, io.Discard)
	}()
	return "127.0.0.1", lp, cancel, nil
}
