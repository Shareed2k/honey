package engine

import (
	"context"
)

type trueNASTransport struct{}

func (t *trueNASTransport) RunCommand(ctx context.Context, user string, tc TargetContext, cache *ClientCache, kvTunnel bool, cmd SSHRemoteCmdFunc, opts BatchOptions) HostExecResult {
	return runOneRemoteTrueNAS(ctx, user, tc, cache, kvTunnel, cmd, opts.RecipeKV, opts.RecipeScopedKV, resolveMaxOutputBytes(opts))
}
