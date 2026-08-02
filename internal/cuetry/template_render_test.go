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

func TestRenderTemplate_shquoteEscapesEmbeddedSingleQuote(t *testing.T) {
	t.Parallel()
	out, err := RenderTemplate(RenderTemplateOpts{
		Template: `echo {{ .val | shquote }}`,
		Data:     map[string]any{"val": "it's; rm -rf ~ #"},
		KV:       noopKV{},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `echo 'it'\''s; rm -rf ~ #'`
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestRenderTemplate_shquoteNonString(t *testing.T) {
	t.Parallel()
	out, err := RenderTemplate(RenderTemplateOpts{
		Template: `{{ .n | shquote }}`,
		Data:     map[string]any{"n": 42},
		KV:       noopKV{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != "'42'" {
		t.Fatalf("got %q", out)
	}
}

// TestRenderTemplate_sprigSquoteIsNotShellSafe documents, as an executable
// regression check, exactly why shquote exists: sprig's own squote does not
// escape an embedded single quote, so piping untrusted data through it and
// into a shell command is not safe (see template_render.go's shquote doc
// comment). If this test starts failing because slim-sprig's squote changed
// to escape embedded quotes, shquote may have become redundant — but don't
// remove it without confirming that upstream behavior deliberately.
func TestRenderTemplate_sprigSquoteIsNotShellSafe(t *testing.T) {
	t.Parallel()
	out, err := RenderTemplate(RenderTemplateOpts{
		Template: `{{ .val | squote }}`,
		Data:     map[string]any{"val": "it's dangerous"},
		KV:       noopKV{},
	})
	if err != nil {
		t.Fatal(err)
	}
	// A correctly shell-escaped value would be 'it'\''s dangerous' — sprig's
	// squote instead produces this broken, shell-unsafe form.
	want := `'it's dangerous'`
	if out != want {
		t.Fatalf("got %q, want %q (if this fails, re-evaluate whether shquote is still needed)", out, want)
	}
}

type noopKV struct{}

func (noopKV) Get(string) (string, bool, error) { return "", false, nil }

type mapKV map[string]string

func (m mapKV) Get(key string) (string, bool, error) {
	v, ok := m[key]
	return v, ok, nil
}
