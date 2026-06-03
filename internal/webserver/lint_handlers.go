package webserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// LintRequest is the JSON body for POST /api/v1/lint.
type LintRequest struct {
	Language string `json:"language"` // "bash" or "python"
	Content  string `json:"content"`
}

// LintDiagnostic is one syntax/lint finding (1-based line/col).
type LintDiagnostic struct {
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	Severity string `json:"severity"` // "error" | "warning"
	Message  string `json:"message"`
}

// LintResponse is the JSON body for a lint result. Available is false when no
// checker tool is installed on the server host (the UI then just highlights).
type LintResponse struct {
	Available   bool             `json:"available"`
	Tool        string           `json:"tool,omitempty"`
	Diagnostics []LintDiagnostic `json:"diagnostics"`
}

const lintTimeout = 5 * time.Second

// handleLint syntax-checks a script with non-executing tooling and returns
// diagnostics for inline display in the web editor.
// @Summary Lint a script (syntax check, non-executing)
// @Tags exec
// @Accept json
// @Produce json
// @Param body body LintRequest true "lint request"
// @Success 200 {object} LintResponse
// @Failure 400 {object} map[string]string
// @Router /api/v1/lint [post]
// @Security BearerAuth
func (s *Server) handleLint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.assistRL.allow(clientIP(r), assistRPM()) {
		httpError(w, fmt.Errorf("rate limit exceeded; try again in a minute"), http.StatusTooManyRequests)
		return
	}
	var body LintRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxWebExecScript)).Decode(&body); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}
	lang := strings.ToLower(strings.TrimSpace(body.Language))
	if lang != "bash" && lang != "python" {
		httpError(w, fmt.Errorf("unsupported language %q (want bash or python)", body.Language), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.Content) == "" {
		writeJSON(w, LintResponse{Available: true, Diagnostics: []LintDiagnostic{}})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), lintTimeout)
	defer cancel()
	resp := lintScript(ctx, lang, body.Content)
	writeJSON(w, resp)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// lintScript writes content to a temp file and runs a non-executing syntax
// checker. It prefers richer tools (shellcheck/pyflakes) and falls back to the
// always-available `bash -n` / `python3 -m py_compile`.
func lintScript(ctx context.Context, lang, content string) LintResponse {
	ext := ".sh"
	if lang == "python" {
		ext = ".py"
	}
	f, err := os.CreateTemp("", "honey-lint-*"+ext)
	if err != nil {
		return LintResponse{Available: false, Diagnostics: []LintDiagnostic{}}
	}
	path := f.Name()
	defer func() { _ = os.Remove(path) }()
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		return LintResponse{Available: false, Diagnostics: []LintDiagnostic{}}
	}
	_ = f.Close()

	var tool, format string
	var args []string
	switch lang {
	case "bash":
		if _, err := exec.LookPath("shellcheck"); err == nil {
			tool, args, format = "shellcheck", []string{"-f", "gcc", path}, "gcc"
		} else if _, err := exec.LookPath("bash"); err == nil {
			tool, args, format = "bash", []string{"-n", path}, "bashN"
		}
	case "python":
		if _, err := exec.LookPath("flake8"); err == nil {
			tool, args, format = "flake8", []string{path}, "flake8"
		} else if _, err := exec.LookPath("pyflakes"); err == nil {
			tool, args, format = "pyflakes", []string{path}, "gcc"
		} else if p, err := exec.LookPath("python3"); err == nil {
			tool, args, format = p, []string{"-m", "py_compile", path}, "pyCompile"
		} else if p, err := exec.LookPath("python"); err == nil {
			tool, args, format = p, []string{"-m", "py_compile", path}, "pyCompile"
		}
	}
	if tool == "" {
		return LintResponse{Available: false, Diagnostics: []LintDiagnostic{}}
	}

	cmd := exec.CommandContext(ctx, tool, args...) // #nosec G204 -- fixed tool + temp path; static syntax check, never executes content
	out, _ := cmd.CombinedOutput()                 // non-zero exit on findings is expected
	text := string(out)
	text = strings.ReplaceAll(text, path, "script")

	var diags []LintDiagnostic
	switch format {
	case "flake8":
		diags = parseFlake8Diagnostics(text)
	case "gcc":
		diags = parseGCCDiagnostics(text)
	case "bashN":
		diags = parseBashNDiagnostics(text)
	default:
		diags = parsePyCompileDiagnostics(text)
	}
	if diags == nil {
		diags = []LintDiagnostic{}
	}
	return LintResponse{Available: true, Tool: tool, Diagnostics: diags}
}

// `path:line:col: severity: message` (shellcheck -f gcc) or `path:line:col: message` (pyflakes).
var gccDiagRe = regexp.MustCompile(`^\s*\S+?:(\d+):(\d+):\s*(?:(note|warning|error|style|info):\s*)?(.*)$`)

func parseGCCDiagnostics(text string) []LintDiagnostic {
	var diags []LintDiagnostic
	for _, line := range strings.Split(text, "\n") {
		m := gccDiagRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		ln, _ := strconv.Atoi(m[1])
		col, _ := strconv.Atoi(m[2])
		sev := "error"
		if m[3] == "warning" || m[3] == "style" || m[3] == "info" || m[3] == "note" {
			sev = "warning"
		}
		diags = append(diags, LintDiagnostic{Line: ln, Col: col, Severity: sev, Message: strings.TrimSpace(m[4])})
	}
	return diags
}

// flake8 default output: `path:line:col: CODE message`. E9** (syntax/runtime) →
// error; style (E/W/C) and pyflakes (F) findings → warning.
var flake8Re = regexp.MustCompile(`^\S+:(\d+):(\d+):\s+(\S+)\s+(.*)$`)

func parseFlake8Diagnostics(text string) []LintDiagnostic {
	var diags []LintDiagnostic
	for _, line := range strings.Split(text, "\n") {
		m := flake8Re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		ln, _ := strconv.Atoi(m[1])
		col, _ := strconv.Atoi(m[2])
		code := m[3]
		sev := "warning"
		if strings.HasPrefix(code, "E9") {
			sev = "error"
		}
		diags = append(diags, LintDiagnostic{Line: ln, Col: col, Severity: sev, Message: strings.TrimSpace(code + " " + m[4])})
	}
	return diags
}

// `bash -n` stderr: `script: line N: <message>`.
var bashNRe = regexp.MustCompile(`line (\d+):\s*(.*)$`)

func parseBashNDiagnostics(text string) []LintDiagnostic {
	var diags []LintDiagnostic
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		m := bashNRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		ln, _ := strconv.Atoi(m[1])
		diags = append(diags, LintDiagnostic{Line: ln, Col: 1, Severity: "error", Message: strings.TrimSpace(m[2])})
	}
	return diags
}

// `python -m py_compile` stderr ends with `File "script", line N` then `<Error>: <msg>`.
var (
	pyFileLineRe = regexp.MustCompile(`File "[^"]*", line (\d+)`)
	pyErrRe      = regexp.MustCompile(`^\s*(\w*Error):\s*(.*)$`)
)

func parsePyCompileDiagnostics(text string) []LintDiagnostic {
	line := 0
	msg := ""
	for _, l := range strings.Split(text, "\n") {
		if m := pyFileLineRe.FindStringSubmatch(l); m != nil {
			line, _ = strconv.Atoi(m[1])
		}
		if m := pyErrRe.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			msg = strings.TrimSpace(m[1] + ": " + m[2])
		}
	}
	if line == 0 && msg == "" {
		return nil
	}
	if line == 0 {
		line = 1
	}
	if msg == "" {
		msg = "syntax error"
	}
	return []LintDiagnostic{{Line: line, Col: 1, Severity: "error", Message: msg}}
}
