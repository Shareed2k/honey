// internal/plugins/plugin_cue_test.go
package plugins

import (
	"slices"
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
