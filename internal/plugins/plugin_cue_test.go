// internal/plugins/plugin_cue_test.go
package plugins

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const testPluginCueSource = `
import "list"

actions: scan: {
	#Config: {
		target: string
		labels?: [string]: string
	}
	argv: list.Concat([
		["image", "--format", "json", config.target],
		[ for k, v in (config & {labels: {}}).labels {"--label=\(k)=\(v)"} ]
	])
	output_format: "json"
}
actions: version: {
	#Config: {}
	argv: ["--version"]
}
actions: connect: {
	#Config: {
		host:     string
		password: string
	}
	argv: ["dbtool", "-h", config.host]
	env: { DB_PASSWORD: config.password }
	output_format: "text"
}
actions: search: {
	#Config: {
		url:   string
		query: string
	}
	argv: ["curl", "-sS", "--data-binary", "@-", config.url]
	stdin: config.query
	output_format: "json"
}
actions: defaults: {
	#Config: {
		field1: string | *"default_opt"
		field2: string | *"default_req"
	}
	argv: ["echo", config.field1, config.field2]
}
`

func TestPluginCue_EvalAction_AppliesDefaults(t *testing.T) {
	pc, err := newPluginCue([]byte(testPluginCueSource))
	if err != nil {
		t.Fatalf("newPluginCue: %v", err)
	}
	// Omit both optional and required fields to ensure defaults are applied
	res, err := pc.evalAction("defaults", map[string]any{})
	if err != nil {
		t.Fatalf("evalAction: %v", err)
	}
	want := []string{"echo", "default_opt", "default_req"}
	if !slices.Equal(res.Argv, want) {
		t.Fatalf("argv=%v want=%v", res.Argv, want)
	}
}

func TestPluginCue_EvalAction_FlatConfig(t *testing.T) {
	pc, err := newPluginCue([]byte(testPluginCueSource))
	if err != nil {
		t.Fatalf("newPluginCue: %v", err)
	}
	res, err := pc.evalAction("scan", map[string]any{"target": "nginx:latest"})
	if err != nil {
		t.Fatalf("evalAction: %v", err)
	}
	want := []string{"image", "--format", "json", "nginx:latest"}
	if !slices.Equal(res.Argv, want) {
		t.Fatalf("argv=%v want=%v", res.Argv, want)
	}
	if res.OutputFormat != "json" {
		t.Fatalf("output_format=%q want json", res.OutputFormat)
	}
}

func TestPluginCue_EvalAction_NestedMapExpandsToRepeatedFlags(t *testing.T) {
	pc, err := newPluginCue([]byte(testPluginCueSource))
	if err != nil {
		t.Fatalf("newPluginCue: %v", err)
	}
	res, err := pc.evalAction("scan", map[string]any{
		"target": "nginx:latest",
		"labels": map[string]any{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("evalAction: %v", err)
	}
	want := []string{"image", "--format", "json", "nginx:latest", "--label=env=prod"}
	if !slices.Equal(res.Argv, want) {
		t.Fatalf("argv=%v want=%v", res.Argv, want)
	}
}

func TestPluginCue_EvalAction_MissingRequiredFieldFails(t *testing.T) {
	pc, err := newPluginCue([]byte(testPluginCueSource))
	if err != nil {
		t.Fatalf("newPluginCue: %v", err)
	}
	if _, err := pc.evalAction("scan", map[string]any{}); err == nil {
		t.Fatal("expected error for missing required config field \"target\"")
	}
}

func TestPluginCue_EvalAction_UnknownAction(t *testing.T) {
	pc, err := newPluginCue([]byte(testPluginCueSource))
	if err != nil {
		t.Fatalf("newPluginCue: %v", err)
	}
	if _, err := pc.evalAction("nope", map[string]any{}); err == nil {
		t.Fatal("expected error for unknown action")
	}
}

func TestPluginCue_EvalAction_DefaultOutputFormatIsText(t *testing.T) {
	pc, err := newPluginCue([]byte(testPluginCueSource))
	if err != nil {
		t.Fatalf("newPluginCue: %v", err)
	}
	res, err := pc.evalAction("version", map[string]any{})
	if err != nil {
		t.Fatalf("evalAction: %v", err)
	}
	if res.OutputFormat != "text" {
		t.Fatalf("output_format=%q want text (default)", res.OutputFormat)
	}
}

func TestNewPluginCue_InvalidSourceFails(t *testing.T) {
	if _, err := newPluginCue([]byte("actions: scan: { argv: [")); err == nil {
		t.Fatal("expected compile error for malformed plugin.cue")
	}
}

func TestPluginCue_EvalAction_EnvFromConfig(t *testing.T) {
	pc, err := newPluginCue([]byte(testPluginCueSource))
	if err != nil {
		t.Fatalf("newPluginCue: %v", err)
	}
	res, err := pc.evalAction("connect", map[string]any{"host": "db.internal", "password": "s3cr3t"})
	if err != nil {
		t.Fatalf("evalAction: %v", err)
	}
	if !slices.Equal(res.Argv, []string{"dbtool", "-h", "db.internal"}) {
		t.Fatalf("argv=%v — password must not leak into argv", res.Argv)
	}
	if res.Env["DB_PASSWORD"] != "s3cr3t" {
		t.Fatalf("env=%v want DB_PASSWORD=s3cr3t", res.Env)
	}
}

func TestPluginCue_EvalAction_DefaultEnvIsEmpty(t *testing.T) {
	pc, err := newPluginCue([]byte(testPluginCueSource))
	if err != nil {
		t.Fatalf("newPluginCue: %v", err)
	}
	res, err := pc.evalAction("version", map[string]any{})
	if err != nil {
		t.Fatalf("evalAction: %v", err)
	}
	if len(res.Env) != 0 {
		t.Fatalf("env=%v want empty for action with no env field", res.Env)
	}
}

func TestPluginCue_EvalAction_StdinFromConfig(t *testing.T) {
	pc, err := newPluginCue([]byte(testPluginCueSource))
	if err != nil {
		t.Fatalf("newPluginCue: %v", err)
	}
	body := `{"query":{"match_all":{}}}`
	res, err := pc.evalAction("search", map[string]any{"url": "https://es.internal/_search", "query": body})
	if err != nil {
		t.Fatalf("evalAction: %v", err)
	}
	if res.Stdin != body {
		t.Fatalf("stdin=%q want %q", res.Stdin, body)
	}
	for _, a := range res.Argv {
		if a == body {
			t.Fatalf("query body must not appear in argv: %v", res.Argv)
		}
	}
}

func TestPluginCue_EvalAction_DefaultStdinIsEmpty(t *testing.T) {
	pc, err := newPluginCue([]byte(testPluginCueSource))
	if err != nil {
		t.Fatalf("newPluginCue: %v", err)
	}
	res, err := pc.evalAction("version", map[string]any{})
	if err != nil {
		t.Fatalf("evalAction: %v", err)
	}
	if res.Stdin != "" {
		t.Fatalf("stdin=%q want empty for action with no stdin field", res.Stdin)
	}
}

// k6PluginCue compiles the real examples/plugins/k6/plugin.cue so these tests
// track the shipped file rather than an inline copy that could drift.
func k6PluginCue(t *testing.T) *pluginCue {
	t.Helper()
	path := filepath.Join("..", "..", "examples", "plugins", "k6", "plugin.cue")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	pc, err := newPluginCue(src)
	if err != nil {
		t.Fatalf("newPluginCue(k6): %v", err)
	}
	return pc
}

func TestPluginCue_K6_RunJSON(t *testing.T) {
	pc := k6PluginCue(t)
	script := "export default function () {}"
	res, err := pc.evalAction("run_json", map[string]any{
		"script":   script,
		"vus":      5,
		"duration": "20s",
	})
	if err != nil {
		t.Fatalf("evalAction: %v", err)
	}
	if res.OutputFormat != "json" {
		t.Fatalf("output_format=%q want json", res.OutputFormat)
	}
	if len(res.Argv) == 0 || res.Argv[0] != "/usr/bin/k6" {
		t.Fatalf("argv[0] must be the absolute in-container path; argv=%v", res.Argv)
	}
	for _, want := range []string{"run", "--vus", "5", "--duration", "20s", "-"} {
		if !slices.Contains(res.Argv, want) {
			t.Fatalf("argv %v missing %q", res.Argv, want)
		}
	}
	// stdin must be the user script plus the appended handleSummary hook so a
	// single JSON document lands on stdout for output_format: "json".
	if !strings.Contains(res.Stdin, script) {
		t.Fatalf("stdin missing user script: %q", res.Stdin)
	}
	if !strings.Contains(res.Stdin, "handleSummary") {
		t.Fatalf("stdin missing handleSummary hook: %q", res.Stdin)
	}
	// env is passed as --env argv flags, never via process env, on this action.
	if len(res.Env) != 0 {
		t.Fatalf("env=%v want empty (env passed as --env argv)", res.Env)
	}
}

func TestPluginCue_K6_RunJSON_IntConfigFromJSON(t *testing.T) {
	pc := k6PluginCue(t)
	// Mirrors the recipe-engine path: config arrives as JSON and is decoded with
	// UseNumber so an integer `vus` unifies with the CUE `int` #Config field.
	// A plain json.Unmarshal would make it float64(5) → CUE 5.0 → "empty
	// disjunction" against `int`.
	cfg, err := decodeConfigJSON([]byte(`{"script":"export default function(){}","vus":5,"duration":"20s"}`))
	if err != nil {
		t.Fatalf("decodeConfigJSON: %v", err)
	}
	res, err := pc.evalAction("run_json", cfg)
	if err != nil {
		t.Fatalf("evalAction: %v", err)
	}
	if !slices.Contains(res.Argv, "5") {
		t.Fatalf("argv %v missing integer vus \"5\"", res.Argv)
	}
	for _, a := range res.Argv {
		if a == "5.0" {
			t.Fatalf("vus rendered as float %q in argv %v", a, res.Argv)
		}
	}
}

func TestPluginCue_K6_RunJSON_MissingScriptFails(t *testing.T) {
	pc := k6PluginCue(t)
	if _, err := pc.evalAction("run_json", map[string]any{}); err == nil {
		t.Fatal("expected error for missing required config field \"script\"")
	}
}

func TestPluginCue_K6_Run_AppliesDefaultsAndEnvFlags(t *testing.T) {
	pc := k6PluginCue(t)
	res, err := pc.evalAction("run", map[string]any{
		"script": "export default function () {}",
		"env":    map[string]any{"TARGET_URL": "https://example.com"},
	})
	if err != nil {
		t.Fatalf("evalAction: %v", err)
	}
	if res.OutputFormat != "text" {
		t.Fatalf("output_format=%q want text", res.OutputFormat)
	}
	// Defaults: vus 1, duration 30s, quiet true.
	for _, want := range []string{"--vus", "1", "--duration", "30s", "--quiet"} {
		if !slices.Contains(res.Argv, want) {
			t.Fatalf("argv %v missing default %q", res.Argv, want)
		}
	}
	// env expands to an adjacent --env K=V pair, not into process env.
	found := false
	for i := 0; i+1 < len(res.Argv); i++ {
		if res.Argv[i] == "--env" && res.Argv[i+1] == "TARGET_URL=https://example.com" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("argv %v missing --env TARGET_URL=... pair", res.Argv)
	}
	if len(res.Env) != 0 {
		t.Fatalf("env=%v want empty (env passed as --env argv)", res.Env)
	}
}
