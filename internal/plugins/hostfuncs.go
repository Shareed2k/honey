package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	extism "github.com/extism/go-sdk"
	"go.uber.org/zap"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
)

const (
	maxHostExecArgs   = 32
	maxHostExecOutput = 8192
	defaultHostExecMS = 30000
)

// registerHostFunc appends a PTR-in/I64-out Extism host function to *fns when
// enabled is true. Every capability-gated host function this package registers
// shares this exact shape (only the callback body differs per capability) —
// concentrating it here removes the copy-pasted registration wiring previously
// repeated at every "if m.AllowX { fns = append(...) }" call site.
func registerHostFunc(fns *[]extism.HostFunction, enabled bool, name string, cb extism.HostFunctionStackCallback) {
	if !enabled {
		return
	}
	*fns = append(*fns, extism.NewHostFunctionWithStack(
		name, cb,
		[]extism.ValueType{extism.ValueTypePTR},
		[]extism.ValueType{extism.ValueTypeI64},
	))
}

// getEnvCallback returns the get_env host function body, permitting only the
// env var names present in allowed.
func getEnvCallback(allowed map[string]struct{}) extism.HostFunctionStackCallback {
	return func(_ context.Context, p *extism.CurrentPlugin, stack []uint64) {
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
	}
}

func hostFunctions(m Manifest, pluginTimeoutMS int) []extism.HostFunction {
	var fns []extism.HostFunction
	// log_info/log_warn are always registered (no capability gate) and return
	// nothing, so they don't fit registerHostFunc's PTR-in/I64-out shape.
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
		if k = strings.TrimSpace(k); k != "" {
			allowed[k] = struct{}{}
		}
	}
	registerHostFunc(&fns, len(allowed) > 0, "get_env", getEnvCallback(allowed))
	registerHostFunc(&fns, m.AllowK8sHTTP, "k8s_http", k8sHTTPCallback(m.ID))

	maxTimeout := pluginTimeoutMS
	if maxTimeout <= 0 {
		maxTimeout = defaultHostExecMS
	}
	registerHostFunc(&fns, m.AllowHostExec, "host_exec", hostExecCallback(m.ID, maxTimeout))
	registerHostFunc(&fns, m.AllowKV, "kv", kvCallback(m.ID))
	registerHostFunc(&fns, m.AllowRemoteExec, "remote_exec", remoteExecCallback(m.ID))
	registerHostFunc(&fns, m.AllowSFTP, "remote_upload", remoteUploadCallback(m.ID))
	registerHostFunc(&fns, m.AllowSFTP, "remote_download", remoteDownloadCallback(m.ID))
	registerHostFunc(&fns, m.AllowSFTP, "remote_stat", remoteStatCallback(m.ID))
	registerHostFunc(&fns, m.AllowTemplateRender, "template_render", templateRenderCallback(m.ID))
	registerHostFunc(&fns, m.AllowPostgres, "postgres_query", postgresQueryCallback(m.ID))
	registerHostFunc(&fns, m.AllowPostgres, "postgres_exec", postgresExecCallback(m.ID))
	registerHostFunc(&fns, m.AllowPostgres, "postgres_migrate", postgresMigrateCallback(m.ID))
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

const (
	defaultK8sHTTPMaxResponseBytes int64 = 4 << 20 // 4 MB
)

func k8sHTTPCallback(pluginID string) extism.HostFunctionStackCallback {
	return func(ctx context.Context, p *extism.CurrentPlugin, stack []uint64) {
		raw, err := p.ReadString(stack[0])
		if err != nil {
			stack[0] = writeK8sHTTPError(p, "read input: "+err.Error())
			return
		}
		var in apiv1.K8sHTTPInput
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			stack[0] = writeK8sHTTPError(p, "parse input: "+err.Error())
			return
		}
		out := runK8sHTTP(ctx, in, pluginID)
		b, err := json.Marshal(out)
		if err != nil {
			stack[0] = writeK8sHTTPError(p, "encode output: "+err.Error())
			return
		}
		off, err := p.WriteString(string(b))
		if err != nil {
			stack[0] = 0
			zap.L().Warn("plugin k8s_http: write output failed", zap.String("plugin_id", pluginID), zap.Error(err))
			return
		}
		stack[0] = off
	}
}

func writeK8sHTTPError(p *extism.CurrentPlugin, msg string) uint64 {
	b, _ := json.Marshal(apiv1.K8sHTTPOutput{Error: msg})
	off, err := p.WriteString(string(b))
	if err != nil {
		return 0
	}
	return off
}

func runK8sHTTP(ctx context.Context, in apiv1.K8sHTTPInput, _ string) apiv1.K8sHTTPOutput {
	hctx, ok := HostRunContextFromContext(ctx)
	if !ok {
		return apiv1.K8sHTTPOutput{Error: "k8s_http: no host context available"}
	}

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kc := hctx.Record.Meta["kubeconfig"]; kc != "" {
		loadingRules.ExplicitPath = kc
	}
	overrides := &clientcmd.ConfigOverrides{}
	if kctx := hctx.Record.Meta["kube_context"]; kctx != "" {
		overrides.CurrentContext = kctx
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
	if err != nil {
		return apiv1.K8sHTTPOutput{Error: fmt.Sprintf("k8s_http: build config for host %q: %s", hctx.Record.Name, err.Error())}
	}

	httpClient, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return apiv1.K8sHTTPOutput{Error: "k8s_http: build http client: " + err.Error()}
	}

	targetURL := strings.TrimRight(cfg.Host, "/") + in.Path
	req, err := http.NewRequestWithContext(ctx, in.Method, targetURL, bytes.NewReader(in.Body))
	if err != nil {
		return apiv1.K8sHTTPOutput{Error: "k8s_http: build request: " + err.Error()}
	}
	for k, v := range in.Headers {
		req.Header.Set(k, v)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return apiv1.K8sHTTPOutput{Error: "k8s_http: do request: " + err.Error()}
	}
	defer resp.Body.Close()

	maxBytes := in.MaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = defaultK8sHTTPMaxResponseBytes
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return apiv1.K8sHTTPOutput{Error: "k8s_http: read response: " + err.Error()}
	}

	respHeaders := make(map[string]string, len(resp.Header))
	for k := range resp.Header {
		respHeaders[k] = resp.Header.Get(k)
	}
	return apiv1.K8sHTTPOutput{
		StatusCode: resp.StatusCode,
		Body:       body,
		Headers:    respHeaders,
	}
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
