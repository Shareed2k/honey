package plugins

import (
	"context"
	"encoding/json"
	"strings"

	extism "github.com/extism/go-sdk"

	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
)

func remoteExecCallback(_ string) extism.HostFunctionStackCallback {
	return func(ctx context.Context, p *extism.CurrentPlugin, stack []uint64) {
		stack[0] = writeRemoteJSON(p, runRemoteExecFromHost(ctx, readHostInput(p, stack)))
	}
}

func runRemoteExecFromHost(ctx context.Context, raw string) any {
	var in apiv1.RemoteExecInput
	if err := parseRemoteInput(raw, &in); err != nil {
		return apiv1.RemoteExecOutput{Failed: true, Error: err.Error()}
	}
	return runRemoteExec(ctx, in)
}

func runRemoteExec(ctx context.Context, in apiv1.RemoteExecInput) apiv1.RemoteExecOutput {
	h, err := remoteHostCtx(ctx)
	if err != nil {
		return apiv1.RemoteExecOutput{Failed: true, Error: err.Error()}
	}
	shell := strings.TrimSpace(in.Shell)
	if shell == "" {
		shell = "/bin/sh"
	}
	script := strings.TrimSpace(in.Script)
	if script == "" {
		return apiv1.RemoteExecOutput{Failed: true, Error: "script is required"}
	}
	if !h.Execute {
		return apiv1.RemoteExecOutput{
			Changed: true,
			Stdout:  dryRunPlan("would run script via "+shell, h),
		}
	}
	if h.Bridge == nil {
		return apiv1.RemoteExecOutput{Failed: true, Error: "remote bridge not configured"}
	}
	out := h.Bridge.RemoteExec(ctx, in)
	out.Stdout = truncateRemoteOutput(out.Stdout)
	out.Stderr = truncateRemoteOutput(out.Stderr)
	return out
}

func readHostInput(p *extism.CurrentPlugin, stack []uint64) string {
	raw, err := p.ReadString(stack[0])
	if err != nil {
		return ""
	}
	return raw
}

func writeRemoteJSON(p *extism.CurrentPlugin, out any) uint64 {
	b, err := json.Marshal(out)
	if err != nil {
		b, _ = json.Marshal(apiv1.RemoteExecOutput{Failed: true, Error: "encode output: " + err.Error()})
	}
	off, err := p.WriteString(string(b))
	if err != nil {
		return 0
	}
	return off
}
