package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
)

func TestHealthzReportsAPIVersion(t *testing.T) {
	rec := httptest.NewRecorder()
	healthzHandler(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body apiv1.HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.APIVersion != apiv1.APIVersion {
		t.Errorf("api_version = %q, want %q", body.APIVersion, apiv1.APIVersion)
	}
}

func TestRunArgv_Success(t *testing.T) {
	resp := runArgv(apiv1.ExecRequest{Argv: []string{"echo", "-n", "hello"}})
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if resp.Output != "hello" {
		t.Fatalf("output=%q want hello", resp.Output)
	}
	if resp.ExitCode != 0 {
		t.Fatalf("exit_code=%d want 0", resp.ExitCode)
	}
}

func TestRunArgv_EmptyArgv(t *testing.T) {
	resp := runArgv(apiv1.ExecRequest{})
	if resp.Error == "" {
		t.Fatal("expected error for empty argv")
	}
}

func TestRunArgv_NonZeroExit(t *testing.T) {
	resp := runArgv(apiv1.ExecRequest{Argv: []string{"sh", "-c", "exit 3"}})
	if resp.ExitCode != 3 {
		t.Fatalf("exit_code=%d want 3", resp.ExitCode)
	}
	if resp.Error != "" {
		t.Fatalf("nonzero exit is not itself an Error (caller inspects ExitCode/Stderr): got %q", resp.Error)
	}
}

func TestRunArgv_CommandNotFound(t *testing.T) {
	resp := runArgv(apiv1.ExecRequest{Argv: []string{"this-binary-does-not-exist-xyz"}})
	if resp.Error == "" {
		t.Fatal("expected error for missing binary")
	}
}

func TestRunArgv_StderrCaptured(t *testing.T) {
	resp := runArgv(apiv1.ExecRequest{Argv: []string{"sh", "-c", "echo oops 1>&2"}})
	if resp.Stderr != "oops\n" {
		t.Fatalf("stderr=%q want %q", resp.Stderr, "oops\n")
	}
}

func TestRunArgv_EnvSetOnChildProcess(t *testing.T) {
	resp := runArgv(apiv1.ExecRequest{
		Argv: []string{"sh", "-c", "printf %s \"$DB_PASSWORD\""},
		Env:  map[string]string{"DB_PASSWORD": "s3cr3t"},
	})
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if resp.Output != "s3cr3t" {
		t.Fatalf("output=%q want s3cr3t — Env must reach the child process", resp.Output)
	}
}

func TestRunArgv_EnvNotSetWithoutRequest(t *testing.T) {
	resp := runArgv(apiv1.ExecRequest{Argv: []string{"sh", "-c", "printf '[%s]' \"$UNSET_TEST_VAR\""}})
	if resp.Output != "[]" {
		t.Fatalf("output=%q want [] — no stray env leakage for an unrequested var", resp.Output)
	}
}
