package engine

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

type localTransport struct{}

func (t *localTransport) RunCommand(ctx context.Context, user string, tc TargetContext, cache *ClientCache, kvTunnel bool, cmd SSHRemoteCmdFunc, opts BatchOptions) HostExecResult {
	res := HostExecResult{
		Name:     tc.Record.Name,
		IP:       tc.Record.PrimaryIP,
		Provider: tc.Record.Provider,
	}

	var kv map[string]string
	if kvTunnel {
		sess, err := opts.RecipeKV.EnsureSession()
		if err != nil {
			res.Success = false
			res.ErrMsg = "kv_tunnel: " + err.Error()
			return res
		}
		kv = map[string]string{
			"HONEY_KV_URL":   sess.LocalBaseURL(),
			"HONEY_KV_TOKEN": sess.Token(),
		}
	}

	remoteCmd := strings.TrimSpace(cmd(tc, kv))

	if opts.CmdTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.CmdTimeout)
		defer cancel()
	}

	execCmd := exec.CommandContext(ctx, "sh", "-c", remoteCmd)

	raw, err := execCmd.CombinedOutput()
	maxOutputBytes := resolveMaxOutputBytes(opts)
	out := strings.TrimSpace(string(raw))
	if maxOutputBytes > 0 && len(out) > maxOutputBytes {
		out = out[:maxOutputBytes] + "\n…(truncated)"
	}
	res.Output = out

	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			res.Success = false
			res.ErrMsg = fmt.Sprintf("command timed out after %s", opts.CmdTimeout)
			return res
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			res.Success = false
			res.ErrMsg = "cancelled"
			return res
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.ExitCode = ee.ExitCode()
			res.Success = false
			if res.ExitCode == 124 {
				if strings.Contains(res.Output, "__HONEY_TIMEOUT_MISSING__") {
					res.ErrMsg = "local host missing `timeout` command (install coreutils or remove step timeout)"
				} else {
					res.ErrMsg = "command timed out (exit 124)"
				}
			} else if res.ExitCode != 0 {
				res.ErrMsg = fmt.Sprintf("exit %d", res.ExitCode)
			}
			return res
		}
		res.Success = false
		res.ErrMsg = err.Error()
		return res
	}

	res.Success = true
	res.ExitCode = 0
	return res
}
