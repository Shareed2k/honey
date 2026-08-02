package engine

import (
	"context"
)

type sshTransport struct{}

func (t *sshTransport) RunCommand(ctx context.Context, user string, tc TargetContext, cache *ClientCache, kvTunnel bool, cmd SSHRemoteCmdFunc, opts BatchOptions) HostExecResult {
	return RunOneRemoteSSH(ctx, user, tc, cache, kvTunnel, cmd, opts.RecipeKV, opts.RecipeScopedKV, opts.CmdTimeout, resolveMaxOutputBytes(opts))
}
