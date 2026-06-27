package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
)

// loadJSManager copies the built js plugin into a temp dir and loads it through
// a real Manager (full WASM round-trip).
func loadJSManager(t *testing.T) *Manager {
	t.Helper()
	root := repoRoot(t)
	src := filepath.Join(root, "plugins", "js")
	wasm := filepath.Join(src, "plugin.wasm")
	if _, err := os.Stat(wasm); err != nil {
		t.Skipf("plugin.wasm not built (run: task build-plugin-modules): %v", err)
	}

	dir := t.TempDir()
	dst := filepath.Join(dir, "js")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"plugin.yaml", "plugin.wasm"} {
		copyTestFile(t, filepath.Join(src, f), filepath.Join(dst, f))
	}

	cfg := config.PluginsEffective{
		Enabled:     true,
		Directory:   dir,
		MaxMemoryMB: 128,
		TimeoutMS:   30000,
	}
	mgr, err := NewManager(t.Context(), cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	if len(mgr.List()) != 1 || mgr.List()[0].ID != "js" {
		t.Fatalf("expected js plugin, got %v", mgr.List())
	}
	return mgr
}

func TestJS_runReturnsJSON(t *testing.T) {
	mgr := loadJSManager(t)
	ctx := WithHostRunContext(t.Context(), &HostRunContext{
		Execute: true,
		Record:  hosts.Record{Name: "web1"},
	})
	in := apiv1.ExecuteStepInput{
		APIVersion: apiv1.APIVersion,
		PluginID:   "js",
		Action:     "run",
		Config:     []byte(`{"code":"args.a + args.b","args":{"a":40,"b":2}}`),
		Execute:    true,
	}
	var out apiv1.ExecuteStepOutput
	if err := mgr.Call(ctx, "js", "execute_step", in, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Success || out.Stdout != "42" {
		t.Fatalf("out=%+v", out)
	}
}

func TestJS_runCallsRemoteExec(t *testing.T) {
	mgr := loadJSManager(t)
	bridge := &fakeRemoteBridge{} // RemoteExec returns Stdout "ok"
	ctx := WithHostRunContext(t.Context(), &HostRunContext{
		Execute: true,
		Record:  hosts.Record{Name: "web1"},
		Bridge:  bridge,
	})
	in := apiv1.ExecuteStepInput{
		APIVersion: apiv1.APIVersion,
		PluginID:   "js",
		Action:     "run",
		Config:     []byte(`{"code":"host.remote_exec('hostname').stdout"}`),
		Execute:    true,
	}
	var out apiv1.ExecuteStepOutput
	if err := mgr.Call(ctx, "js", "execute_step", in, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Success {
		t.Fatalf("run failed: %+v", out)
	}
	if !bridge.execCalled || bridge.lastExec.Script != "hostname" {
		t.Fatalf("bridge=%+v", bridge)
	}
	if out.Stdout != "ok" {
		t.Fatalf("stdout=%q want ok", out.Stdout)
	}
}

func TestJS_dryRunSkipsRemoteExec(t *testing.T) {
	mgr := loadJSManager(t)
	bridge := &fakeRemoteBridge{}
	ctx := WithHostRunContext(t.Context(), &HostRunContext{
		Execute: false,
		Record:  hosts.Record{Name: "web1"},
		Bridge:  bridge,
	})
	in := apiv1.ExecuteStepInput{
		APIVersion: apiv1.APIVersion,
		PluginID:   "js",
		Action:     "run",
		Config:     []byte(`{"code":"host.remote_exec('rm -rf /').stdout"}`),
		Execute:    false,
	}
	var out apiv1.ExecuteStepOutput
	if err := mgr.Call(ctx, "js", "execute_step", in, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Success {
		t.Fatalf("dry-run failed: %+v", out)
	}
	if bridge.execCalled {
		t.Fatal("dry-run must NOT call remote_exec")
	}
}

func TestJS_runtimeErrorFails(t *testing.T) {
	mgr := loadJSManager(t)
	ctx := WithHostRunContext(t.Context(), &HostRunContext{Execute: true, Record: hosts.Record{Name: "web1"}})
	in := apiv1.ExecuteStepInput{
		APIVersion: apiv1.APIVersion,
		PluginID:   "js",
		Action:     "run",
		Config:     []byte(`{"code":"throw new Error('boom')"}`),
		Execute:    true,
	}
	var out apiv1.ExecuteStepOutput
	if err := mgr.Call(ctx, "js", "execute_step", in, &out); err != nil {
		t.Fatal(err)
	}
	if out.Success || !strings.Contains(out.Err, "boom") {
		t.Fatalf("expected failure mentioning boom, got %+v", out)
	}
}

func TestJS_unknownAction(t *testing.T) {
	mgr := loadJSManager(t)
	ctx := WithHostRunContext(t.Context(), &HostRunContext{Record: hosts.Record{Name: "web1"}})
	in := apiv1.ExecuteStepInput{
		APIVersion: apiv1.APIVersion,
		PluginID:   "js",
		Action:     "bogus",
	}
	var out apiv1.ExecuteStepOutput
	if err := mgr.Call(ctx, "js", "execute_step", in, &out); err != nil {
		t.Fatal(err)
	}
	if out.Success || !strings.Contains(out.Err, "unknown action") {
		t.Fatalf("expected unknown-action failure, got %+v", out)
	}
}
