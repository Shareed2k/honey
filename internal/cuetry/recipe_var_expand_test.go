package cuetry

import (
	"strings"
	"testing"
)

func TestExpandRecipeVars_basic(t *testing.T) {
	t.Parallel()
	out, err := ExpandRecipeVars("hello ${NAME}", map[string]string{"NAME": "world"}, true)
	if err != nil || out != "hello world" {
		t.Fatalf("got %q err=%v", out, err)
	}
}

func TestExpandRecipeVars_unknownStrict(t *testing.T) {
	t.Parallel()
	_, err := ExpandRecipeVars("${MISSING}", map[string]string{}, true)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExpandRecipeVars_unknownPassthrough(t *testing.T) {
	t.Parallel()
	out, err := ExpandRecipeVars("${MISSING}", map[string]string{}, false)
	if err != nil || out != "${MISSING}" {
		t.Fatalf("got %q err=%v", out, err)
	}
}

func TestExpandRecipeVarsInData(t *testing.T) {
	t.Parallel()
	data := map[string]any{"greeting": "hi ${WHO}"}
	if err := ExpandRecipeVarsInData(data, map[string]string{"WHO": "there"}, true); err != nil {
		t.Fatal(err)
	}
	if data["greeting"] != "hi there" {
		t.Fatalf("%v", data)
	}
}

func TestPrepareTemplateData_envFromAndVar(t *testing.T) {
	t.Parallel()
	store := NewStepOutputStore()
	store.Record("fetch", "web-1", "alice")
	outputCap := NewRecipeOutputCapture()
	step := &StepBase{
		EnvFrom: []EnvFromRef{{Step: "fetch", Map: map[string]string{"HOSTNAME": "stdout"}}},
	}
	data := map[string]any{"msg": "hello ${HOSTNAME}"}
	if err := PrepareTemplateData(data, step, store, outputCap, nil, "web-1", nil, false, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(data["msg"].(string), "alice") {
		t.Fatalf("%v", data)
	}
}
