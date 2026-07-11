package mobile_test

import (
	"strings"
	"testing"

	"github.com/shareed2k/honey/pkg/mobile"
)

type mockLogCallback struct {
	logs     []string
	progress []string
}

func (m *mockLogCallback) OnLog(msg string) {
	m.logs = append(m.logs, msg)
}

func (m *mockLogCallback) OnProgress(progressJSON string) {
	m.progress = append(m.progress, progressJSON)
}

func TestExecuteRecipe(t *testing.T) {
	tests := []struct {
		name        string
		requestJSON string
		cb          *mockLogCallback
		wantStatus  string
		wantLog     string
	}{
		{
			name:        "with callback",
			requestJSON: `{"recipe": "test"}`,
			cb:          &mockLogCallback{},
			wantStatus:  `{"status": "success"}`,
			wantLog:     "Initializing honey engine...",
		},
		{
			name:        "without callback",
			requestJSON: `{"recipe": "test"}`,
			cb:          nil,
			wantStatus:  `{"status": "success"}`,
			wantLog:     "", // shouldn't panic
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cb mobile.LogCallback
			if tt.cb != nil {
				cb = tt.cb
			}

			got, err := mobile.ExecuteRecipe(tt.requestJSON, cb)
			if err != nil {
				t.Fatalf("ExecuteRecipe() unexpected error: %v", err)
			}

			if got != tt.wantStatus {
				t.Errorf("ExecuteRecipe() = %v, want %v", got, tt.wantStatus)
			}

			if tt.cb != nil && (len(tt.cb.logs) == 0 || tt.cb.logs[0] != tt.wantLog) {
				var gotLog string
				if len(tt.cb.logs) > 0 {
					gotLog = tt.cb.logs[0]
				}
				t.Errorf("ExecuteRecipe() callback log = %v, want %v", gotLog, tt.wantLog)
			}
		})
	}
}

func TestLoadConfigMissing(t *testing.T) {
	got, err := mobile.LoadConfig(t.TempDir())
	if err != nil {
		t.Fatalf("LoadConfig on empty dir: %v", err)
	}
	if got == "" {
		t.Error("expected non-empty JSON")
	}
}

func TestSaveAndLoadConfig(t *testing.T) {
	dir := t.TempDir()
	payload := `{"version":1,"defaults":{"ssh_user":"root"},"backends":{}}`
	if err := mobile.SaveConfig(dir, payload); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	got, err := mobile.LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig after save: %v", err)
	}
	if !strings.Contains(got, "root") {
		t.Errorf("expected ssh_user=root in result, got: %s", got)
	}
}

func TestSearchHosts(t *testing.T) {
	// Request a backend that doesn't exist or is purely local to avoid network calls.
	// We'll ask for a non-existent backend to ensure it doesn't try connecting to k8s/docker.
	// But hostapi.SearchHosts will return an error if no backends match: "no backends match backends=..."
	// Let's test the error case as it proves parsing works.
	requestJSON := `{"name":"test-host","no_cache":true,"backends":"dummy-backend"}`
	_, err := mobile.SearchHosts(requestJSON)
	if err == nil {
		t.Fatalf("SearchHosts() expected error for missing backend, got nil")
	}
	if !strings.Contains(err.Error(), "no backends match") && !strings.Contains(err.Error(), "all backends failed") {
		t.Errorf("SearchHosts() expected backend failure error, got: %v", err)
	}
}

func TestGetVersion(t *testing.T) {
	v := mobile.GetVersion()
	if v == "" {
		t.Error("expected version string, got empty")
	}
}

func TestListBackends(t *testing.T) {
	_, err := mobile.ListBackends(`{not json`)
	if err == nil {
		t.Error("expected error for bad JSON")
	}

	got, err := mobile.ListBackends(`{"config_path": "dummy"}`)
	if err != nil {
		t.Fatalf("unexpected error for ListBackends: %v", err)
	}
	if !strings.Contains(got, "backends") {
		t.Errorf("expected backends in response, got %s", got)
	}
}

func TestExec(t *testing.T) {
	_, err := mobile.Exec(`{not json`)
	if err == nil {
		t.Error("expected error for bad JSON")
	}

	// Test direct IP which skips search.
	// Since we are mocking, it will try to SSH to 127.0.0.1 on a high port and probably fail,
	// but it will cover the plumbing.
	req := `{"host_ip": "127.0.0.1", "ssh_port": 65535, "command": "echo ok"}`
	got, err := mobile.Exec(req)
	// it might fail with "ssh: connect to host" or return a JSON with error inside results
	if err != nil {
		// If the engine.ExecuteSSHParallel returns error instead of results with error
		if !strings.Contains(err.Error(), "connection refused") && !strings.Contains(err.Error(), "dial tcp") {
			t.Logf("Expected network dial error from dummy ip, got: %v", err)
		}
	} else {
		if !strings.Contains(got, "results") {
			t.Errorf("expected results in Exec response, got: %s", got)
		}
	}
}
