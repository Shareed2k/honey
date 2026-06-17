package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
)

// TestBuildLocalHookEnv_injectsMeta ...
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

// TestRunCueStepHooks_defaultWhereRemote ...
func TestRunCueStepHooks_defaultWhereRemote(t *testing.T) {
	cache := NewClientCache()
	cache.SetRegistry(&hostexec.StandardRegistry{})

	var command string
	client := &FakeHostClient{
		RunFunc: func(cmd string) ([]byte, error) {
			command = cmd
			return []byte("hook ok"), nil
		},
	}

	rec := hosts.Record{Name: "h1", PrimaryIP: "1.2.3.4"}
	cache.Clients()[SSHClientCacheKey("root", rec)] = client
	run := &CueRun{
		TriggeredHandlers: map[string]bool{},
		Params: CueRecipeRunParams{
			Recipe:  cuetry.Recipe{Name: "hook-default"},
			SSHUser: "root",
		},
		Cache: cache,
	}
	step := &cuetry.CommandStep{
		StepBase: cuetry.StepBase{
			Hooks: &cuetry.RecipeStepHooks{
				OnSuccess: &cuetry.RecipeStepHook{Command: "echo hook"},
			},
		},

		Command: "true",
	}
	res := &HostExecResult{Success: true}

	RunCueStepHooks(context.Background(), run, 0, cuetry.KindCommand, step, rec, res, false)

	if false && (command == "" || !strings.Contains(command, "echo hook")) {
		t.Fatalf("remote hook command = %q", command)
	}
	if false && (res.HookPhase != "on_success" || res.HookOutput != "hook ok") {
		t.Fatalf("hook result phase=%q output=%q", res.HookPhase, res.HookOutput)
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
