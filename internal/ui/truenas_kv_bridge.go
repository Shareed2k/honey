package ui

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/truenasprovider"
	"github.com/shareed2k/honey/internal/sshclient"
	"github.com/shareed2k/honey/internal/stepkv"
	"github.com/shareed2k/honey/internal/truenasshell"
)

func truenasApplianceSSHForwardEligible(r hosts.Record) bool {
	return r.Provider == "truenas" &&
		strings.EqualFold(strings.TrimSpace(r.Meta["kind"]), "appliance") &&
		strings.TrimSpace(r.PrimaryIP) != ""
}

// attachTrueNASKVTunnel returns HONEY_KV_* for TrueNAS API shell command steps.
func attachTrueNASKVTunnel(ctx context.Context, user string, r hosts.Record, kvTunnel, recipeScoped bool, recipeKV *RecipeKVCoordinator, cache *ClientCache) (map[string]string, func(), error) {
	if !kvTunnel {
		return nil, nil, nil
	}
	if recipeScoped && recipeKV != nil {
		env, err := recipeKV.EnsureTrueNASAPIShellBridgeEnv(ctx, user, r, cache)
		if err != nil {
			return nil, nil, err
		}
		return env, nil, nil
	}
	kvSess, err := stepkv.Start(stepKVTunnelTTL)
	if err != nil {
		return nil, nil, err
	}
	env, stop, err := attachTrueNASKVForRecord(ctx, user, r, kvSess, cache)
	if err != nil {
		_ = kvSess.Close()
		return nil, nil, err
	}
	stopAll := func() {
		if stop != nil {
			stop()
		}
		_ = kvSess.Close()
	}
	return env, stopAll, nil
}

func attachTrueNASKVForRecord(ctx context.Context, user string, r hosts.Record, kvSess *stepkv.Session, cache *ClientCache) (map[string]string, func(), error) {
	if kvSess == nil {
		return nil, nil, errors.New("truenas kv: nil stepkv session")
	}
	if truenasApplianceSSHForwardEligible(r) && cache != nil {
		if hc, err := cache.GetOrDial(user, r); err == nil {
			if c, ok := hc.(*sshclient.HoneyClient); ok {
				if env, stop, err := attachKVRemoteForwardToSession(c, kvSess); err == nil {
					return env, stop, nil
				}
			}
		}
	}
	b, ok := truenasprovider.BackendByName(r.Meta["backend_name"])
	if !ok {
		return nil, nil, fmt.Errorf("truenas backend not configured")
	}
	return startTrueNASAPIShellKVBridge(ctx, b, r, kvSess)
}

// EnsureTrueNASAPIShellBridgeEnv attaches kv_tunnel for a TrueNAS row into the shared recipe stepkv session.
func (c *RecipeKVCoordinator) EnsureTrueNASAPIShellBridgeEnv(ctx context.Context, user string, r hosts.Record, cache *ClientCache) (map[string]string, error) {
	if c == nil {
		return nil, errors.New("recipe kv: nil coordinator")
	}
	return c.ensureForward(user, r, func(sess *stepkv.Session) (map[string]string, func(), error) {
		return attachTrueNASKVForRecord(ctx, user, r, sess, cache)
	})
}

func startTrueNASAPIShellKVBridge(ctx context.Context, b truenasprovider.TrueNASBackendRuntime, rec hosts.Record, kvSess *stepkv.Session) (map[string]string, func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	local := kvSess.LocalTCPAddr()
	if local == "" {
		return nil, nil, fmt.Errorf("stepkv: empty local dial address")
	}

	shellSess, err := truenasshell.OpenSession(ctx, b, rec, 24, 120)
	if err != nil {
		return nil, nil, err
	}

	prOut, pwOut := io.Pipe()
	prIn, pwIn := io.Pipe()
	bridgeCtx, cancel := context.WithCancel(ctx)

	pumpOutDone := make(chan struct{})
	pumpInDone := make(chan struct{})
	go pumpTrueNASShellToPipe(shellSess, pwOut, pumpOutDone)
	go pumpPipeToTrueNASShell(prIn, shellSess, pumpInDone)

	pyB64 := base64.StdEncoding.EncodeToString([]byte(k8sKVExecBridgePodPy))
	bootstrap := fmt.Sprintf("\nstty -echo 2>/dev/null; printf %%s %s | base64 -d > /tmp/honey-kv-bridge.py && exec python3 -u /tmp/honey-kv-bridge.py\n",
		shellSingleQuoted(pyB64))
	if err := shellSess.WriteBinary([]byte(bootstrap)); err != nil {
		cancel()
		_ = pwOut.Close()
		_ = pwIn.Close()
		<-pumpOutDone
		<-pumpInDone
		_ = shellSess.Close()
		return nil, nil, fmt.Errorf("truenas kv bridge bootstrap: %w", err)
	}

	ready := make(chan int, 1)
	bridgeErrCh := make(chan error, 1)
	bridgeDone := make(chan struct{})

	go runHkvBridgeLoop(bridgeCtx, prOut, pwIn, local, ready, bridgeErrCh, bridgeDone)

	stop := func() {
		cancel()
		_ = pwOut.Close()
		<-bridgeDone
		_ = pwIn.Close()
		<-pumpOutDone
		<-pumpInDone
		_ = shellSess.Close()
	}

	select {
	case port := <-ready:
		env := map[string]string{
			"HONEY_KV_URL":   fmt.Sprintf("http://127.0.0.1:%d", port),
			"HONEY_KV_TOKEN": kvSess.Token(),
		}
		return env, stop, nil
	case err := <-bridgeErrCh:
		stop()
		if err != nil {
			return nil, nil, fmt.Errorf("truenas kv bridge: %w", err)
		}
		return nil, nil, errors.New("truenas kv bridge: exited before READY")
	case <-bridgeDone:
		stop()
		return nil, nil, errors.New("truenas kv bridge: bridge exited before READY")
	case <-time.After(45 * time.Second):
		stop()
		return nil, nil, errors.New("truenas kv bridge: READY timeout")
	case <-ctx.Done():
		stop()
		return nil, nil, ctx.Err()
	}
}

func pumpTrueNASShellToPipe(sess *truenasshell.Session, pwOut *io.PipeWriter, done chan struct{}) {
	defer close(done)
	defer func() { _ = pwOut.Close() }()
	for {
		mt, data, err := sess.ReadMessage()
		if err != nil {
			_ = pwOut.CloseWithError(err)
			return
		}
		if mt == websocket.TextMessage || mt == websocket.BinaryMessage {
			if _, werr := pwOut.Write(data); werr != nil {
				return
			}
		}
	}
}

func pumpPipeToTrueNASShell(prIn io.Reader, sess *truenasshell.Session, done chan struct{}) {
	defer close(done)
	buf := make([]byte, 32<<10)
	for {
		n, err := prIn.Read(buf)
		if n > 0 {
			if werr := sess.WriteBinary(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// readTrueNASBridgeReady scans shell output for the python bridge READY line (used in tests).
func readTrueNASBridgeReady(br *bufio.Reader) (int, error) {
	return readReadyLine(br)
}
