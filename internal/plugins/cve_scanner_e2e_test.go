package plugins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
)

// scriptBridge is a RemoteBridge whose RemoteExec returns canned output keyed on
// what the executed script contains, and records the last script it saw.
type scriptBridge struct {
	lastScript string
	scanRows   string
}

func (b *scriptBridge) RemoteExec(_ context.Context, in apiv1.RemoteExecInput) apiv1.RemoteExecOutput {
	b.lastScript = in.Script
	// A patch script always declares the package-manager case statement.
	if strings.Contains(in.Script, "package manager:") {
		return apiv1.RemoteExecOutput{ExitCode: 0, Stdout: "package manager: apt\nupgraded 2", Changed: true}
	}
	// Otherwise treat it as a scan and return the canned scanner rows.
	return apiv1.RemoteExecOutput{ExitCode: 0, Stdout: b.scanRows}
}

func (b *scriptBridge) RemoteUpload(context.Context, apiv1.RemoteUploadInput) apiv1.RemoteUploadOutput {
	return apiv1.RemoteUploadOutput{}
}

func (b *scriptBridge) RemoteDownload(context.Context, apiv1.RemoteDownloadInput) apiv1.RemoteDownloadOutput {
	return apiv1.RemoteDownloadOutput{}
}

func (b *scriptBridge) RemoteStat(context.Context, apiv1.RemoteStatInput) apiv1.RemoteStatOutput {
	return apiv1.RemoteStatOutput{Exists: true}
}

func (b *scriptBridge) TemplateRender(_ context.Context, in apiv1.TemplateRenderInput) apiv1.TemplateRenderOutput {
	return apiv1.TemplateRenderOutput{Content: in.Template}
}

// loadCVEScannerManager copies the built cve-scanner plugin into a temp dir and
// loads it through a real Manager (full WASM round-trip).
func loadCVEScannerManager(t *testing.T) *Manager {
	t.Helper()
	root := repoRoot(t)
	src := filepath.Join(root, "plugins", "cve-scanner")
	wasm := filepath.Join(src, "plugin.wasm")
	if _, err := os.Stat(wasm); err != nil {
		t.Skipf("plugin.wasm not built (run: task build-plugin-modules): %v", err)
	}

	dir := t.TempDir()
	dst := filepath.Join(dir, "cve-scanner")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"plugin.yaml", "plugin.wasm"} {
		copyTestFile(t, filepath.Join(src, f), filepath.Join(dst, f))
	}

	cfg := config.PluginsEffective{
		Enabled:     true,
		Directory:   dir,
		MaxMemoryMB: 64,
		TimeoutMS:   30000,
	}
	mgr, err := NewManager(t.Context(), cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	if len(mgr.List()) != 1 || mgr.List()[0].ID != "cve-scanner" {
		t.Fatalf("expected cve-scanner plugin, got %v", mgr.List())
	}
	return mgr
}

func copyTestFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for d := cwd; d != "/"; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	t.Fatalf("no go.mod above %s", cwd)
	return ""
}

func TestCVEScanner_scanDryRun(t *testing.T) {
	mgr := loadCVEScannerManager(t)
	ctx := WithHostRunContext(t.Context(), &HostRunContext{
		Execute: false,
		Record:  hosts.Record{Name: "web1"},
	})
	in := apiv1.ExecuteStepInput{
		APIVersion: apiv1.APIVersion,
		PluginID:   "cve-scanner",
		Action:     "scan",
		Config:     []byte(`{"scanner":"grype","target":"dir:/srv"}`),
		Execute:    false,
	}
	var out apiv1.ExecuteStepOutput
	if err := mgr.Call(ctx, "cve-scanner", "execute_step", in, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Success || !strings.Contains(out.Stdout, "would scan dir:/srv with grype") {
		t.Fatalf("out=%+v", out)
	}
}

func TestCVEScanner_scanExecuteNormalizesReport(t *testing.T) {
	mgr := loadCVEScannerManager(t)
	bridge := &scriptBridge{
		scanRows: "CVE-2024-0001|Critical|openssl|3.0.2|3.0.13\n" +
			"CVE-2024-0002|High|libc6|2.35|2.36\n" +
			"GHSA-aaaa-bbbb|Medium|leftpad|1.0.0|\n",
	}
	ctx := WithHostRunContext(t.Context(), &HostRunContext{
		Execute: true,
		Record:  hosts.Record{Name: "web1"},
		Bridge:  bridge,
	})
	in := apiv1.ExecuteStepInput{
		APIVersion: apiv1.APIVersion,
		PluginID:   "cve-scanner",
		Action:     "scan",
		Config:     []byte(`{"scanner":"grype","target":"dir:/","min_severity":"high"}`),
		Execute:    true,
	}
	var out apiv1.ExecuteStepOutput
	if err := mgr.Call(ctx, "cve-scanner", "execute_step", in, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Success {
		t.Fatalf("scan failed: %+v", out)
	}
	// Bridge must have run the grype scan script.
	if !strings.Contains(bridge.lastScript, "grype") {
		t.Fatalf("scan script not grype: %q", bridge.lastScript)
	}

	var rep struct {
		Total      int            `json:"total"`
		BySeverity map[string]int `json:"by_severity"`
		CVEs       []struct {
			ID       string `json:"id"`
			Severity string `json:"severity"`
			Package  string `json:"package"`
			Fixed    string `json:"fixed"`
		} `json:"cves"`
	}
	if err := json.Unmarshal([]byte(out.Stdout), &rep); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, out.Stdout)
	}
	// min_severity=high drops the medium GHSA finding -> 2 remain.
	if rep.Total != 2 {
		t.Fatalf("total=%d want 2: %+v", rep.Total, rep.CVEs)
	}
	if rep.CVEs[0].Severity != "critical" || rep.CVEs[0].Package != "openssl" {
		t.Fatalf("first finding=%+v", rep.CVEs[0])
	}
	if rep.BySeverity["critical"] != 1 || rep.BySeverity["high"] != 1 {
		t.Fatalf("by_severity=%v", rep.BySeverity)
	}
}

func TestCVEScanner_patchRunsManagerScript(t *testing.T) {
	mgr := loadCVEScannerManager(t)
	bridge := &scriptBridge{}
	ctx := WithHostRunContext(t.Context(), &HostRunContext{
		Execute: true,
		Record:  hosts.Record{Name: "web1"},
		Bridge:  bridge,
	})
	in := apiv1.ExecuteStepInput{
		APIVersion: apiv1.APIVersion,
		PluginID:   "cve-scanner",
		Action:     "patch",
		Config:     []byte(`{"manager":"apt","security_only":true}`),
		Execute:    true,
	}
	var out apiv1.ExecuteStepOutput
	if err := mgr.Call(ctx, "cve-scanner", "execute_step", in, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Success || !out.Changed {
		t.Fatalf("patch out=%+v", out)
	}
	for _, want := range []string{"package manager:", "apt-get update", "case \"$MGR\""} {
		if !strings.Contains(bridge.lastScript, want) {
			t.Fatalf("patch script missing %q:\n%s", want, bridge.lastScript)
		}
	}
}

func TestCVEScanner_unknownAction(t *testing.T) {
	mgr := loadCVEScannerManager(t)
	ctx := WithHostRunContext(t.Context(), &HostRunContext{Record: hosts.Record{Name: "web1"}})
	in := apiv1.ExecuteStepInput{
		APIVersion: apiv1.APIVersion,
		PluginID:   "cve-scanner",
		Action:     "bogus",
	}
	var out apiv1.ExecuteStepOutput
	if err := mgr.Call(ctx, "cve-scanner", "execute_step", in, &out); err != nil {
		t.Fatal(err)
	}
	if out.Success || !strings.Contains(out.Err, "unknown action") {
		t.Fatalf("expected unknown-action failure, got %+v", out)
	}
}
