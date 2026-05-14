package ui

import (
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
)

func TestBuildLocalHookEnv_injectsMeta(t *testing.T) {
	hook := &cuetry.RecipeStepHook{
		Env: map[string]string{"FOO": "bar"},
	}
	stepRes := HostExecResult{Success: true, ExitCode: 0, ErrMsg: "none"}
	r := hosts.Record{Name: "web-1", PrimaryIP: "10.0.0.5", Provider: "aws", Zone: "a"}
	env, err := buildLocalHookEnv("demo", 3, "on_success", stepRes, r, hook)
	if err != nil {
		t.Fatal(err)
	}
	m := parseEnvSlice(env)
	if m["HONEY_HOST_NAME"] != "web-1" || m["HONEY_HOST_PRIMARY_IP"] != "10.0.0.5" || m["HONEY_HOST_PROVIDER"] != "aws" || m["HONEY_HOST_ZONE"] != "a" {
		t.Fatalf("host meta: %#v", m)
	}
	if m["HONEY_HOOK_STEP"] != "3" || m["HONEY_HOOK_PHASE"] != "on_success" || m["HONEY_HOST_STEP_SUCCESS"] != "true" || m["HONEY_HOST_EXIT_CODE"] != "0" {
		t.Fatalf("hook meta: %#v", m)
	}
	if m["HONEY_RECIPE_NAME"] != "demo" || m["FOO"] != "bar" {
		t.Fatalf("extras: %#v", m)
	}
}

func parseEnvSlice(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, kv := range env {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			continue
		}
		out[kv[:i]] = kv[i+1:]
	}
	return out
}
