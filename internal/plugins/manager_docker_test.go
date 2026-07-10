package plugins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
)

// fakeTransport is a minimal pluginTransport double so Manager.Call's
// nonzero-exit-vs-PluginError precedence can be tested without a real
// Extism or Docker transport underneath.
type fakeTransport struct {
	exit     int
	outBytes []byte
	err      error
}

func (f fakeTransport) CallRaw(_ context.Context, _ string, _ []byte) (int, []byte, error) {
	return f.exit, f.outBytes, f.err
}

func (f fakeTransport) Close(_ context.Context) error { return nil }

// TestManagerCall_NonZeroExitPrefersPluginErrorText proves that when a
// transport returns a nonzero exit code AND a decodable apiv1.PluginError in
// outBytes (as dockerTransport.CallRaw deliberately does for its nonzero-exit
// case), Manager.Call surfaces that descriptive PluginError.Error text
// instead of the generic "plugin returned exit code N" message.
func TestManagerCall_NonZeroExitPrefersPluginErrorText(t *testing.T) {
	pe := apiv1.PluginError{Error: "custom failure detail"}
	outBytes, err := json.Marshal(pe)
	if err != nil {
		t.Fatalf("marshal PluginError: %v", err)
	}

	m := &Manager{
		enabled: true,
		byID: map[string]*loadedPlugin{
			"fake": {
				manifest:  Manifest{ID: "fake"},
				transport: fakeTransport{exit: 1, outBytes: outBytes},
			},
		},
	}

	callErr := m.Call(context.Background(), "fake", "export", map[string]any{}, nil)
	if callErr == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(callErr.Error(), "custom failure detail") {
		t.Fatalf("error=%q want it to contain %q", callErr.Error(), "custom failure detail")
	}
	if strings.Contains(callErr.Error(), "exit code") {
		t.Fatalf("error=%q should not fall back to generic exit-code message when PluginError.Error is present", callErr.Error())
	}
}

// TestManagerCall_ExecuteStepNonZeroExitDoesNotBecomeError closes the loop
// between "dockerTransport.CallRaw is correct in isolation" and "the fix
// actually works end-to-end through Manager.Call" — the code path that
// actually decides whether a nonzero exit becomes an error or clean data.
// CallRaw's execute_step path deliberately reports a normal, expected step
// failure (the exec'd program ran and exited nonzero) as exit=0/err=nil with
// an apiv1.ExecuteStepOutput{Success:false, ...} envelope in outBytes, so
// that Manager.Call decodes it straight into the caller's *out instead of
// treating it as a failed call. This test wires that exact shape into a fake
// transport (same fakeTransport seam TestManagerCall_NonZeroExitPrefersPluginErrorText
// uses) and drives it through the real Manager.Call, not CallRaw directly.
func TestManagerCall_ExecuteStepNonZeroExitDoesNotBecomeError(t *testing.T) {
	stepOut := apiv1.ExecuteStepOutput{Success: false, ExitCode: 7, Stderr: "boom", Err: "boom"}
	outBytes, err := json.Marshal(stepOut)
	if err != nil {
		t.Fatalf("marshal ExecuteStepOutput: %v", err)
	}

	m := &Manager{
		enabled: true,
		byID: map[string]*loadedPlugin{
			"fake": {
				manifest:  Manifest{ID: "fake"},
				transport: fakeTransport{exit: 0, outBytes: outBytes},
			},
		},
	}

	in := apiv1.ExecuteStepInput{Action: "broken", Config: []byte("{}")}
	var out apiv1.ExecuteStepOutput
	if callErr := m.Call(context.Background(), "fake", "execute_step", in, &out); callErr != nil {
		t.Fatalf("Manager.Call: %v (expected nil — exit=0 with ExecuteStepOutput data must not become an error)", callErr)
	}
	if out.Success {
		t.Fatal("out.Success=true want false")
	}
	if out.ExitCode != 7 {
		t.Fatalf("out.ExitCode=%d want 7", out.ExitCode)
	}
}

func TestLoadPluginDir_DockerRuntimeMissingCueFile(t *testing.T) {
	dir := t.TempDir()
	const yaml = `id: trivy
version: "0.1.0"
capabilities:
  - custom_step
runtime: docker
docker:
  image: "aquasec/trivy:0.72.0"
`
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	// Intentionally no plugin.cue written.
	_, err := loadPluginDir(t.Context(), dir, PluginsFromConfig(nil))
	if err == nil {
		t.Fatal("expected error when runtime: docker plugin is missing plugin.cue")
	}
	if !strings.Contains(err.Error(), "plugin.cue") {
		t.Fatalf("error=%q should originate from the plugin.cue read in loadDockerPluginDir, not from loadManifest's capabilities check", err.Error())
	}
}

func TestLocatePluginInitBinary_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	fakeBinary := filepath.Join(dir, "honey-plugin-init")
	if err := os.WriteFile(fakeBinary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HONEY_PLUGIN_INIT_PATH", fakeBinary)

	got, err := locatePluginInitBinary()
	if err != nil {
		t.Fatalf("locatePluginInitBinary: %v", err)
	}
	if got != fakeBinary {
		t.Fatalf("got=%q want=%q", got, fakeBinary)
	}
}

func TestResolveAllowedEnv_ResolvesSetVarsOmitsUnset(t *testing.T) {
	t.Setenv("HONEY_TEST_ALLOWED_ENV_VAR", "value1")
	got := resolveAllowedEnv([]string{"HONEY_TEST_ALLOWED_ENV_VAR", "HONEY_TEST_DEFINITELY_UNSET_VAR", ""})
	want := map[string]string{"HONEY_TEST_ALLOWED_ENV_VAR": "value1"}
	if len(got) != len(want) || got["HONEY_TEST_ALLOWED_ENV_VAR"] != "value1" {
		t.Fatalf("resolveAllowedEnv=%v want=%v", got, want)
	}
}

func TestLocatePluginInitBinary_MissingFailsClearly(t *testing.T) {
	t.Setenv("HONEY_PLUGIN_INIT_PATH", "")
	// Without the env override, this falls back to a path next to the test
	// binary itself, which won't have honey-plugin-init sitting next to it.
	if _, err := locatePluginInitBinary(); err == nil {
		t.Fatal("expected error when honey-plugin-init isn't found anywhere")
	}
}
