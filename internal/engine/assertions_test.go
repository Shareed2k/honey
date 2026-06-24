package engine

import (
	"testing"

	"github.com/shareed2k/honey/internal/cuetry"
)

func TestEvaluateAssertions(t *testing.T) {
	codeOne := 1

	tests := []struct {
		name       string
		result     HostExecResult
		assertions []cuetry.Assertion
		wantErr    bool
		wantSucc   bool
	}{
		{
			name:       "exit code override success",
			result:     HostExecResult{Success: false, ExitCode: 1, Output: "error"},
			assertions: []cuetry.Assertion{{ExitCode: &codeOne}},
			wantErr:    false,
			wantSucc:   true,
		},
		{
			name:       "exit code fail",
			result:     HostExecResult{Success: true, ExitCode: 0, Output: "ok"},
			assertions: []cuetry.Assertion{{ExitCode: &codeOne}},
			wantErr:    true,
			wantSucc:   false,
		},
		{
			name:       "regex success",
			result:     HostExecResult{Success: true, Output: "hello world"},
			assertions: []cuetry.Assertion{{Regex: "hello.*"}},
			wantErr:    false,
			wantSucc:   true,
		},
		{
			name:       "regex fail",
			result:     HostExecResult{Success: true, Output: "hello world"},
			assertions: []cuetry.Assertion{{Regex: "goodbye"}},
			wantErr:    true,
			wantSucc:   false,
		},
		{
			name:       "not regex fail",
			result:     HostExecResult{Success: true, Output: "error occurred"},
			assertions: []cuetry.Assertion{{NotRegex: "error"}},
			wantErr:    true,
			wantSucc:   false,
		},
		{
			name:       "json path existence",
			result:     HostExecResult{Success: true, Output: `{"status":"ok"}`},
			assertions: []cuetry.Assertion{{JSONPath: "status"}},
			wantErr:    false,
			wantSucc:   true,
		},
		{
			name:       "json path value success",
			result:     HostExecResult{Success: true, Output: `{"status":"ready"}`},
			assertions: []cuetry.Assertion{{JSONPath: "status", ExpectedValue: "ready"}},
			wantErr:    false,
			wantSucc:   true,
		},
		{
			name:       "json path value fail",
			result:     HostExecResult{Success: true, Output: `{"status":"pending"}`},
			assertions: []cuetry.Assertion{{JSONPath: "status", ExpectedValue: "ready"}},
			wantErr:    true,
			wantSucc:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := tt.result
			err := EvaluateAssertions(&res, tt.assertions)
			if (err != nil) != tt.wantErr {
				t.Errorf("EvaluateAssertions() error = %v, wantErr %v", err, tt.wantErr)
			}
			if res.Success != tt.wantSucc {
				t.Errorf("EvaluateAssertions() Success = %v, want %v", res.Success, tt.wantSucc)
			}
		})
	}
}
