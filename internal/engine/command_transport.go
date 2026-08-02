package engine

import (
	"context"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/truenasshell"
)

// HostTransport defines the seam for executing a shell command on a target record.
// Implementations map to specific execution environments (SSH, TrueNAS, Local OS).
type HostTransport interface {
	RunCommand(ctx context.Context, user string, tc TargetContext, cache *ClientCache, kvTunnel bool, cmd SSHRemoteCmdFunc, opts BatchOptions) HostExecResult
}

// resolveTransport examines the TargetContext and returns the appropriate adapter.
func resolveTransport(tc TargetContext) HostTransport {
	if tc.Record.Name == cuetry.MatchLocalAIHost && tc.Record.PrimaryIP == "-" {
		return &localTransport{}
	}
	if tc.Record.Provider == "truenas" && truenasshell.ShouldUseTrueNASShell(tc.Record, truenasshell.ConsoleTrueNASAPI) {
		return &trueNASTransport{}
	}
	return &sshTransport{}
}
