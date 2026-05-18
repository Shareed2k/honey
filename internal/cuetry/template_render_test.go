package cuetry

import (
	"strings"
	"testing"
)

func TestRenderTemplate_basic(t *testing.T) {
	t.Parallel()
	out, err := RenderTemplate(RenderTemplateOpts{
		Template: "hello {{ .name }}",
		Data:     map[string]any{"name": "world"},
		KV:       noopKV{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello world" {
		t.Fatalf("got %q", out)
	}
}

func TestRenderTemplate_preservesDollarLiteral(t *testing.T) {
	t.Parallel()
	out, err := RenderTemplate(RenderTemplateOpts{
		Template: "export FOO=${BAR}",
		Data:     map[string]any{},
		KV:       noopKV{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "${BAR}") {
		t.Fatalf("got %q", out)
	}
}

func TestRenderTemplate_missingKey(t *testing.T) {
	t.Parallel()
	_, err := RenderTemplate(RenderTemplateOpts{
		Template: "{{ .missing }}",
		Data:     map[string]any{},
		KV:       noopKV{},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRenderTemplate_splitJoin(t *testing.T) {
	t.Parallel()
	out, err := RenderTemplate(RenderTemplateOpts{
		Template: `{{ "a,b" | split "," | join ";" }}`,
		Data:     map[string]any{},
		KV:       noopKV{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != "a;b" {
		t.Fatalf("got %q", out)
	}
}

func TestRenderTemplate_kvGet(t *testing.T) {
	t.Parallel()
	out, err := RenderTemplate(RenderTemplateOpts{
		Template: `{{ kvGet "deploy-status" | default "unknown" }}`,
		Data:     map[string]any{},
		KV:       mapKV{"deploy-status": "ready"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != "ready" {
		t.Fatalf("got %q", out)
	}
}

type noopKV struct{}

func (noopKV) Get(string) (string, bool, error) { return "", false, nil }

type mapKV map[string]string

func (m mapKV) Get(key string) (string, bool, error) {
	v, ok := m[key]
	return v, ok, nil
}
