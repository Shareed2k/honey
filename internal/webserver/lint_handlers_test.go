package webserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"
)

func TestParseGCCDiagnostics(t *testing.T) {
	t.Parallel()
	// shellcheck -f gcc style (with severity) + pyflakes style (no severity).
	in := "script:3:5: warning: foo is referenced but not assigned [SC2154]\n" +
		"script:7:1: error: bad thing\n" +
		"script:10:2: undefined name 'x'\n"
	got := parseGCCDiagnostics(in)
	if len(got) != 3 {
		t.Fatalf("want 3 diagnostics, got %d: %+v", len(got), got)
	}
	if got[0].Line != 3 || got[0].Col != 5 || got[0].Severity != "warning" {
		t.Fatalf("diag0 = %+v", got[0])
	}
	if got[1].Severity != "error" || got[1].Line != 7 {
		t.Fatalf("diag1 = %+v", got[1])
	}
	if got[2].Line != 10 || got[2].Severity != "error" || got[2].Message != "undefined name 'x'" {
		t.Fatalf("diag2 = %+v", got[2])
	}
}

func TestParseFlake8Diagnostics(t *testing.T) {
	t.Parallel()
	in := "script:1:1: F401 'os' imported but unused\n" +
		"script:2:1: E999 SyntaxError: invalid syntax\n"
	got := parseFlake8Diagnostics(in)
	if len(got) != 2 {
		t.Fatalf("want 2 diagnostics, got %d: %+v", len(got), got)
	}
	if got[0].Line != 1 || got[0].Severity != "warning" || got[0].Message != "F401 'os' imported but unused" {
		t.Fatalf("diag0 = %+v", got[0])
	}
	if got[1].Line != 2 || got[1].Severity != "error" || got[1].Message != "E999 SyntaxError: invalid syntax" {
		t.Fatalf("diag1 = %+v", got[1])
	}
}

func TestParseBashNDiagnostics(t *testing.T) {
	t.Parallel()
	in := "script: line 5: syntax error: unexpected end of file\n"
	got := parseBashNDiagnostics(in)
	if len(got) != 1 || got[0].Line != 5 || got[0].Severity != "error" {
		t.Fatalf("got %+v", got)
	}
}

func TestParsePyCompileDiagnostics(t *testing.T) {
	t.Parallel()
	in := "Sorry: IndentationError: ...\n  File \"script\", line 2\n    def f(:\n          ^\nSyntaxError: invalid syntax\n"
	got := parsePyCompileDiagnostics(in)
	if len(got) != 1 || got[0].Line != 2 || got[0].Severity != "error" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Message != "SyntaxError: invalid syntax" {
		t.Fatalf("msg = %q", got[0].Message)
	}
}

func TestHandleLintRejectsBadLanguage(t *testing.T) {
	t.Parallel()
	s, err := NewServer(Options{ListenAddr: "127.0.0.1:0", Token: "tok", Version: "0"})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(LintRequest{Language: "ruby", Content: "x"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/lint", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer tok")
	s.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestLintScriptBashSyntaxError(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
	// Unterminated if → syntax error from bash -n or shellcheck.
	resp := lintScript(context.Background(), "bash", "if true; then\n")
	if !resp.Available {
		t.Fatal("expected lint available with bash present")
	}
	if len(resp.Diagnostics) == 0 {
		t.Fatalf("expected diagnostics for unterminated if, got none (tool=%s)", resp.Tool)
	}
}

func TestLintScriptBashClean(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
	resp := lintScript(context.Background(), "bash", "echo hello\n")
	if !resp.Available {
		t.Fatal("expected available")
	}
	// shellcheck may warn; bash -n should be clean. Accept 0 errors for bash -n.
	if resp.Tool == "bash" && len(resp.Diagnostics) != 0 {
		t.Fatalf("clean script should have no bash -n diagnostics, got %+v", resp.Diagnostics)
	}
}
