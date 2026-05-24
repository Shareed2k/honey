package cuetry

import (
	"context"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/stepkv"
)

func TestCompileWhen_valid(t *testing.T) {
	t.Parallel()
	if _, err := CompileWhen(`host.name != "" && execute`); err != nil {
		t.Fatal(err)
	}
}

func TestCompileWhen_invalid(t *testing.T) {
	t.Parallel()
	_, err := CompileWhen(`host.name +`)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEvalWhen_hostMeta(t *testing.T) {
	t.Parallel()
	prog, err := CompileWhen(`host.meta['role'] == 'web'`)
	if err != nil {
		t.Fatal(err)
	}
	host := hosts.Record{
		Name: "web-1",
		Meta: map[string]string{"role": "web"},
	}
	ok, err := EvalWhen(prog, WhenEvalOpts{
		Host:    host,
		Execute: true,
		Steps:   map[string]StepView{},
		Secrets: map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected true")
	}
}

func TestEvalWhen_stepsStdout(t *testing.T) {
	t.Parallel()
	prog, err := CompileWhen(`steps['fetch'].stdout.contains('shard')`)
	if err != nil {
		t.Fatal(err)
	}
	store := NewStepResultStore()
	store.RecordHost("fetch", "web-1", HostStepResult{Succeeded: true, Stdout: "shard-web-1"})
	ok, err := EvalWhen(prog, WhenEvalOpts{
		Host:    hosts.Record{Name: "web-1"},
		Execute: true,
		Steps:   store.StepsViewForHost("web-1"),
		Secrets: map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected true")
	}
}

func TestEvalWhen_kvFunctions(t *testing.T) {
	t.Parallel()
	sess, err := stepkv.Start(0)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if err := sess.Put("graph_seed_web-1_ready", "1"); err != nil {
		t.Fatal(err)
	}
	prog, err := CompileWhen(`kv_has('graph_seed_web-1_ready') && kv_get('graph_seed_web-1_ready') == '1'`)
	if err != nil {
		t.Fatal(err)
	}
	kv := stepkvSessionKVTest{sess}
	ok, err := EvalWhen(prog, WhenEvalOpts{
		Host:    hosts.Record{Name: "web-1"},
		Execute: true,
		Steps:   map[string]StepView{},
		Secrets: map[string]string{},
		KV:      kv,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected true")
	}
}

type stepkvSessionKVTest struct{ sess *stepkv.Session }

func (s stepkvSessionKVTest) Get(key string) (string, bool, error) {
	return s.sess.Get(key)
}

func TestEvalWhen_env(t *testing.T) {
	t.Parallel()
	prog, err := CompileWhen(`env['BARMAN_DO_RESET'] == 'true'`)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := EvalWhen(prog, WhenEvalOpts{
		Host:    hosts.Record{Name: "barman-1"},
		Execute: true,
		Steps:   map[string]StepView{},
		Secrets: map[string]string{},
		Env:     map[string]string{"BARMAN_DO_RESET": "true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected true")
	}
	ok, err = EvalWhen(prog, WhenEvalOpts{
		Host:    hosts.Record{Name: "barman-1"},
		Execute: true,
		Steps:   map[string]StepView{},
		Secrets: map[string]string{},
		Env:     map[string]string{"BARMAN_DO_RESET": "false"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected false")
	}
}

func TestBuildEnvMapForWhen_cliOverride(t *testing.T) {
	t.Parallel()
	defaults := &RecipeDefaults{
		Env: map[string]string{"BARMAN_DO_RESET": "false"},
	}
	host := hosts.Record{Name: "barman-1", PrimaryIP: "10.0.0.1"}
	m, err := BuildEnvMapForWhen(context.Background(), false, nil, RecipeStep{}, defaults, map[string]string{"BARMAN_DO_RESET": "true"}, &host)
	if err != nil {
		t.Fatal(err)
	}
	if m["BARMAN_DO_RESET"] != "true" {
		t.Fatalf("got %q want true", m["BARMAN_DO_RESET"])
	}
}

func TestBuildSecretsMapForWhen_dryRun(t *testing.T) {
	t.Parallel()
	m, err := BuildSecretsMapForWhen(context.Background(), false, nil, RecipeStep{
		Secrets: map[string]string{"X": "secure:v1:AA:bb"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(m["X"], "<<secret ") {
		t.Fatalf("got %q", m["X"])
	}
}

func TestValidateStepWhen_requiresID(t *testing.T) {
	t.Parallel()
	err := validateStepWhen(0, ExecutionModeGraph, RecipeStep{
		Host: "*",
		When: "true",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "id") {
		t.Fatalf("got %v", err)
	}
}

func TestParseRemoteRecipe_graphWhen(t *testing.T) {
	t.Parallel()
	cue := `
recipe: {
	name: "g"
	type: "graph"
	steps: [
		{ id: "fetch", host: "*", command: "echo" },
		{ id: "use", host: "*", depends: ["fetch"], when: "steps['fetch'].succeeded", command: "echo" },
	]
}`
	r, err := ParseRemoteRecipe([]byte(cue), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(r.Steps[1].When) == "" {
		t.Fatal("expected when")
	}
}

func TestValidateRecipeGraph_linearWhenID(t *testing.T) {
	t.Parallel()
	r := Recipe{
		Name: "l",
		Steps: []RecipeStep{{
			Host:    "*",
			Command: "echo",
			When:    "host.name != ''",
		}},
	}
	if err := ValidateRecipeGraph(r); err == nil {
		t.Fatal("expected id required")
	}
}
