package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	extism "github.com/extism/go-sdk"
	"go.uber.org/zap"

	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
)

const (
	maxHostExecArgs   = 32
	maxHostExecOutput = 8192
	defaultHostExecMS = 30000
)

func hostFunctions(m Manifest, pluginTimeoutMS int) []extism.HostFunction {
	var fns []extism.HostFunction
	fns = append(fns, extism.NewHostFunctionWithStack(
		"log_info",
		func(_ context.Context, p *extism.CurrentPlugin, stack []uint64) {
			msg, err := p.ReadString(stack[0])
			if err != nil {
				return
			}
			zap.L().Info("plugin", zap.String("plugin_id", m.ID), zap.String("msg", msg))
		},
		[]extism.ValueType{extism.ValueTypePTR},
		[]extism.ValueType{},
	))
	fns = append(fns, extism.NewHostFunctionWithStack(
		"log_warn",
		func(_ context.Context, p *extism.CurrentPlugin, stack []uint64) {
			msg, err := p.ReadString(stack[0])
			if err != nil {
				return
			}
			zap.L().Warn("plugin", zap.String("plugin_id", m.ID), zap.String("msg", msg))
		},
		[]extism.ValueType{extism.ValueTypePTR},
		[]extism.ValueType{},
	))
	allowed := make(map[string]struct{}, len(m.AllowedEnv))
	for _, k := range m.AllowedEnv {
		k = strings.TrimSpace(k)
		if k != "" {
			allowed[k] = struct{}{}
		}
	}
	if len(allowed) > 0 {
		fns = append(fns, extism.NewHostFunctionWithStack(
			"get_env",
			func(_ context.Context, p *extism.CurrentPlugin, stack []uint64) {
				key, err := p.ReadString(stack[0])
				if err != nil {
					stack[0] = 0
					return
				}
				if _, ok := allowed[key]; !ok {
					stack[0] = 0
					return
				}
				val := os.Getenv(key)
				off, err := p.WriteString(val)
				if err != nil {
					stack[0] = 0
					return
				}
				stack[0] = off
			},
			[]extism.ValueType{extism.ValueTypePTR},
			[]extism.ValueType{extism.ValueTypeI64},
		))
	}
	if m.AllowHostExec {
		maxTimeout := pluginTimeoutMS
		if maxTimeout <= 0 {
			maxTimeout = defaultHostExecMS
		}
		fns = append(fns, extism.NewHostFunctionWithStack(
			"host_exec",
			hostExecCallback(m.ID, maxTimeout),
			[]extism.ValueType{extism.ValueTypePTR},
			[]extism.ValueType{extism.ValueTypeI64},
		))
	}
	if m.AllowKV {
		fns = append(fns, extism.NewHostFunctionWithStack(
			"kv",
			kvCallback(m.ID),
			[]extism.ValueType{extism.ValueTypePTR},
			[]extism.ValueType{extism.ValueTypeI64},
		))
	}
	return fns
}

func kvCallback(pluginID string) extism.HostFunctionStackCallback {
	return func(ctx context.Context, p *extism.CurrentPlugin, stack []uint64) {
		raw, err := p.ReadString(stack[0])
		if err != nil {
			stack[0] = writeKVError(p, "read input: "+err.Error())
			return
		}
		var in apiv1.KVInput
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			stack[0] = writeKVError(p, "parse input: "+err.Error())
			return
		}
		out := runKV(ctx, in)
		b, err := json.Marshal(out)
		if err != nil {
			stack[0] = writeKVError(p, "encode output: "+err.Error())
			return
		}
		off, err := p.WriteString(string(b))
		if err != nil {
			stack[0] = 0
			zap.L().Warn("plugin kv: write output failed", zap.String("plugin_id", pluginID), zap.Error(err))
			return
		}
		stack[0] = off
	}
}

func writeKVError(p *extism.CurrentPlugin, msg string) uint64 {
	b, _ := json.Marshal(apiv1.KVOutput{Error: msg})
	off, err := p.WriteString(string(b))
	if err != nil {
		return 0
	}
	return off
}

func runKV(ctx context.Context, in apiv1.KVInput) apiv1.KVOutput {
	sess, ok := KVSessionFromContext(ctx)
	if !ok {
		return apiv1.KVOutput{Error: "kv not available for this call"}
	}
	op := strings.ToLower(strings.TrimSpace(in.Op))
	key := strings.TrimSpace(in.Key)
	switch op {
	case "get":
		val, found, err := sess.Get(key)
		if err != nil {
			return apiv1.KVOutput{Error: err.Error()}
		}
		return apiv1.KVOutput{Found: found, Value: val}
	case "put":
		if err := sess.Put(key, in.Value); err != nil {
			return apiv1.KVOutput{Error: err.Error()}
		}
		return apiv1.KVOutput{}
	case "delete":
		if err := sess.Delete(key); err != nil {
			return apiv1.KVOutput{Error: err.Error()}
		}
		return apiv1.KVOutput{}
	default:
		return apiv1.KVOutput{Error: "unknown op " + in.Op + " (want get, put, delete)"}
	}
}

// RunKVForTest exposes runKV for unit tests.
func RunKVForTest(ctx context.Context, in apiv1.KVInput) apiv1.KVOutput {
	return runKV(ctx, in)
}

func hostExecCallback(pluginID string, maxTimeoutMS int) extism.HostFunctionStackCallback {
	return func(ctx context.Context, p *extism.CurrentPlugin, stack []uint64) {
		raw, err := p.ReadString(stack[0])
		if err != nil {
			stack[0] = writeHostExecError(p, "read input: "+err.Error())
			return
		}
		var in apiv1.HostExecInput
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			stack[0] = writeHostExecError(p, "parse input: "+err.Error())
			return
		}
		out := runHostExec(ctx, in, maxTimeoutMS)
		b, err := json.Marshal(out)
		if err != nil {
			stack[0] = writeHostExecError(p, "encode output: "+err.Error())
			return
		}
		off, err := p.WriteString(string(b))
		if err != nil {
			stack[0] = 0
			zap.L().Warn("plugin host_exec: write output failed", zap.String("plugin_id", pluginID), zap.Error(err))
			return
		}
		stack[0] = off
	}
}

func writeHostExecError(p *extism.CurrentPlugin, msg string) uint64 {
	b, _ := json.Marshal(apiv1.HostExecOutput{ExitCode: -1, Error: msg})
	off, err := p.WriteString(string(b))
	if err != nil {
		return 0
	}
	return off
}

func runHostExec(ctx context.Context, in apiv1.HostExecInput, maxTimeoutMS int) apiv1.HostExecOutput {
	if len(in.Argv) == 0 {
		return apiv1.HostExecOutput{ExitCode: -1, Error: "argv is required"}
	}
	if len(in.Argv) > maxHostExecArgs {
		return apiv1.HostExecOutput{ExitCode: -1, Error: fmt.Sprintf("argv exceeds %d args", maxHostExecArgs)}
	}
	timeoutMS := in.TimeoutMS
	if timeoutMS <= 0 {
		timeoutMS = maxTimeoutMS
	}
	if timeoutMS > maxTimeoutMS {
		timeoutMS = maxTimeoutMS
	}
	hctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(hctx, in.Argv[0], in.Argv[1:]...) // #nosec G204 -- argv-only subprocess for trusted plugins with allow_host_exec
	if cwd := strings.TrimSpace(in.Cwd); cwd != "" {
		abs, err := filepath.Abs(cwd)
		if err != nil {
			return apiv1.HostExecOutput{ExitCode: -1, Error: "cwd: " + err.Error()}
		}
		cmd.Dir = abs
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := apiv1.HostExecOutput{
		Stdout: truncateHostExec(stdout.String()),
		Stderr: truncateHostExec(stderr.String()),
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			out.ExitCode = ee.ExitCode()
		} else {
			out.ExitCode = -1
			out.Error = err.Error()
		}
		return out
	}
	out.ExitCode = 0
	return out
}

func truncateHostExec(s string) string {
	if len(s) <= maxHostExecOutput {
		return s
	}
	return s[:maxHostExecOutput]
}

// hostFunctionNames returns registered host function export names (for tests).
func hostFunctionNames(m Manifest) []string {
	fns := hostFunctions(m, defaultHostExecMS)
	names := make([]string, 0, len(fns))
	for _, f := range fns {
		names = append(names, f.Name)
	}
	return names
}
