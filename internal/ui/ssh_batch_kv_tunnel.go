package ui

import (
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/sshclient"
)

// attachHostKVTunnel resolves HONEY_KV_* for kv_tunnel on this pooled client. stopKV is non-nil only for
// non–recipe-scoped SSH (per-exec stepkv forward); recipe-scoped SSH/k8s use the coordinator and return stopKV nil.
func attachHostKVTunnel(client HostClient, user string, r hosts.Record, recipeScopedKV bool, recipeKV *RecipeKVCoordinator) (kv map[string]string, stopKV func(), errMsg string) {
	switch c := client.(type) {
	case *sshclient.HoneyClient:
		if recipeScopedKV {
			if recipeKV == nil {
				return nil, nil, "kv_tunnel: recipe-scoped coordinator is missing"
			}
			env, err := recipeKV.EnsureKVTunnelEnv(user, r, c)
			if err != nil {
				return nil, nil, "kv_tunnel: " + err.Error()
			}
			return env, nil, ""
		}
		env, st, err := attachStepKVRemoteForward(c, stepKVTunnelTTL)
		if err != nil {
			return nil, nil, "kv_tunnel: " + err.Error()
		}
		return env, st, ""
	case *k8sNativeClient:
		if recipeScopedKV && recipeKV == nil {
			return nil, nil, "kv_tunnel: recipe-scoped coordinator is missing"
		}
		if recipeScopedKV && recipeKV != nil {
			env, err := recipeKV.EnsureK8sExecBridgeEnv(user, r, c)
			if err != nil {
				return nil, nil, "kv_tunnel: " + err.Error()
			}
			return env, nil, ""
		}
		return nil, nil, ""
	default:
		return nil, nil, "kv_tunnel is not supported for this executor"
	}
}

// maybeWrapK8sKVShell wraps remoteCmd for per-exec in-pod kv_tunnel when the client is k8s and kv is still nil.
func maybeWrapK8sKVShell(kvTunnel bool, client HostClient, kv map[string]string, remoteCmd string) (string, error) {
	if !kvTunnel {
		return remoteCmd, nil
	}
	if _, ok := client.(*k8sNativeClient); !ok {
		return remoteCmd, nil
	}
	if kv != nil {
		return remoteCmd, nil
	}
	return wrapK8sPodKVShell(remoteCmd)
}
