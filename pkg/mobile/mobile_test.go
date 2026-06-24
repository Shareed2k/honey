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
