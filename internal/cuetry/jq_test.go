package cuetry

import (
	"encoding/json"
	"testing"
)

func TestEvalJQ_scalar(t *testing.T) {
	t.Parallel()
	doc := `[{"n":3,"ts":"2026-05-19T14:30:00Z"}]`
	got, err := EvalJQ(doc, ".[0].n")
	if err != nil {
		t.Fatal(err)
	}
	if got != "3" {
		t.Fatalf("got %q", got)
	}
}

func TestEvalJQ_multi(t *testing.T) {
	t.Parallel()
	doc := `[{"usename":"app"},{"usename":"postgres"}]`
	got, err := EvalJQ(doc, ".[].usename")
	if err != nil {
		t.Fatal(err)
	}
	if got != `["app","postgres"]` {
		t.Fatalf("got %q", got)
	}
}

func TestExpandPluginConfigJSON(t *testing.T) {
	t.Parallel()
	cfg := []byte(`{"sql":"SELECT $1","params":["${THRESHOLD}"]}`)
	out, err := ExpandPluginConfigJSON(cfg, map[string]string{"THRESHOLD": "5"}, false)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	params, ok := parsed["params"].([]any)
	if !ok || len(params) != 1 || params[0] != "5" {
		t.Fatalf("params %v", parsed["params"])
	}
}

func TestMergeEnvFromInto_extract(t *testing.T) {
	t.Parallel()
	store := NewStepOutputStore()
	store.Record("pg", "h1", `[{"n":7}]`)
	step := &StepBase{
		EnvFrom: []EnvFromRef{{
			Step:    "pg",
			Extract: map[string]string{"THRESHOLD": ".[0].n"},
		}},
	}
	dst := map[string]string{}
	if err := MergeEnvFromInto(dst, step, store, nil, nil, "h1", false); err != nil {
		t.Fatal(err)
	}
	if dst["THRESHOLD"] != "7" {
		t.Fatalf("got %v", dst)
	}
}

type testKVReader map[string]string

func (m testKVReader) Get(key string) (string, bool, error) {
	v, ok := m[key]
	return v, ok, nil
}

func TestMergeEnvFromInto_kv(t *testing.T) {
	t.Parallel()
	step := &StepBase{
		EnvFrom: []EnvFromRef{{
			Kv: map[string]string{"THRESHOLD": "pg_activity_count"},
		}},
	}
	dst := map[string]string{}
	kv := testKVReader{"pg_activity_count": "9"}
	if err := MergeEnvFromInto(dst, step, nil, nil, kv, "h1", false); err != nil {
		t.Fatal(err)
	}
	if dst["THRESHOLD"] != "9" {
		t.Fatalf("got %v", dst)
	}
}

func TestEvalJQArray(t *testing.T) {
	t.Parallel()
	doc := `[{"name":"alice"},{"name":"bob"}]`
	got, err := EvalJQArray(doc, ".[].name")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "alice" || got[1] != "bob" {
		t.Fatalf("expected [alice bob], got %v", got)
	}
}
