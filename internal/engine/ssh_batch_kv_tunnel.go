package engine

import (
	"errors"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/sshclient"
)

var errMissingRecipeKVCoordinator = errors.New("recipe-scoped coordinator is missing")

// KVTunnelProvider is an optional interface that HostClients can implement
// if they natively support or explicitly reject bootstrapping KV tunnels.
// KVTunnelProvider ...
type KVTunnelProvider interface {
	SupportsKVTunnel() bool
}

// attachHostKVTunnel resolves HONEY_KV_* for kv_tunnel on this pooled client. stopKV is non-nil only for
// non–recipe-scoped SSH (per-exec stepkv forward); recipe-scoped SSH/k8s use the coordinator and return stopKV nil.
// The error is unprefixed; callers are expected to wrap with %q semantics if they want.
func attachHostKVTunnel(client HostClient, user string, r hosts.Record, recipeScopedKV bool, recipeKV *RecipeKVCoordinator) (kv map[string]string, stopKV func(), err error) {
	if provider, ok := client.(KVTunnelProvider); ok && !provider.SupportsKVTunnel() {
		return nil, nil, nil
	}

	switch c := client.(type) {
	case *sshclient.HoneyClient:
		if !recipeScopedKV {
			return attachStepKVRemoteForward(c, stepKVTunnelTTL)
		}
		if recipeKV == nil {
			return nil, nil, errMissingRecipeKVCoordinator
		}
		env, err := recipeKV.EnsureKVTunnelEnv(user, r, c)
		if err != nil {
			return nil, nil, err
		}
		return env, nil, nil

	case *K8sNativeClient:
		if !recipeScopedKV {
			return nil, nil, nil
		}
		if recipeKV == nil {
			return nil, nil, errMissingRecipeKVCoordinator
		}
		env, err := recipeKV.EnsureK8sExecBridgeEnv(user, r, c)
		if err != nil {
			return nil, nil, err
		}
		return env, nil, nil

	default:
		return nil, nil, errors.New("kv_tunnel is not supported for this executor")
	}
}

// maybeWrapK8sKVShell wraps remoteCmd for per-exec in-pod kv_tunnel when the client is k8s and kv is still nil.
func maybeWrapK8sKVShell(kvTunnel bool, client HostClient, kv map[string]string, remoteCmd string) (string, error) {
	if !kvTunnel {
		return remoteCmd, nil
	}
	if _, ok := client.(*K8sNativeClient); !ok {
		return remoteCmd, nil
	}
	if kv != nil {
		return remoteCmd, nil
	}
	return wrapK8sPodKVShell(remoteCmd)
}
