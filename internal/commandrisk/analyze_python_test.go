package commandrisk

import "testing"

func TestAnalyzePython(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantCritical bool
		wantSignal   string // expected signal id ("" → expect no signals)
	}{
		{
			name:         "os.system shell critical",
			src:          `os.system("rm -rf /")`,
			wantCritical: true,
			wantSignal:   "DELETE_ROOT_PATH",
		},
		{
			name:         "subprocess list shell critical",
			src:          `subprocess.run(["rm", "-rf", "/etc"])`,
			wantCritical: true,
			wantSignal:   "DELETE_ROOT_PATH",
		},
		{
			name:         "rmtree system path",
			src:          `shutil.rmtree("/")`,
			wantCritical: true,
			wantSignal:   "PYTHON_RMTREE_SYSTEM_PATH",
		},
		{
			name:       "rmtree non-system path",
			src:        `shutil.rmtree("/tmp/cache")`,
			wantSignal: "PYTHON_RMTREE",
		},
		{
			name:       "eval dynamic exec",
			src:        `eval(user_input)`,
			wantSignal: "PYTHON_DYNAMIC_EXEC",
		},
		{
			name:         "open block device",
			src:          `open("/dev/sda", "wb")`,
			wantCritical: true,
			wantSignal:   "DD_WRITE_BLOCK_DEVICE",
		},
		{
			name:       "dynamic shell exec",
			src:        `os.system(cmd)`,
			wantSignal: "PYTHON_DYNAMIC_SHELL_EXEC",
		},
		{
			name:       "benign",
			src:        "print(\"hi\")\nfor i in range(3):\n    print(i)",
			wantSignal: "",
		},
		{
			name:       "parse error",
			src:        "def (:",
			wantSignal: "PYTHON_PARSE_INCOMPLETE",
		},
		{
			// gpython has no f-string support; this must NOT raise a medium signal.
			name:       "fstring degrades to low",
			src:        "for i in range(3):\n\tprint(f\"x {i}\")\n",
			wantSignal: "PYTHON_PARSE_INCOMPLETE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := analyzePython(tt.src)
			if a.Critical != tt.wantCritical {
				t.Errorf("Critical = %v, want %v (signals: %+v)", a.Critical, tt.wantCritical, a.Signals)
			}
			if tt.wantSignal == "" {
				if len(a.Signals) != 0 {
					t.Errorf("expected no signals, got %+v", a.Signals)
				}
				return
			}
			if !hasSignal(a, tt.wantSignal) {
				t.Errorf("missing signal %q, got %+v", tt.wantSignal, a.Signals)
			}
		})
	}
}

func TestAnalyzeStep(t *testing.T) {
	t.Run("shell interpreter uses shell analyzer", func(t *testing.T) {
		a := AnalyzeStep("rm -rf /", "bash")
		if !a.Critical || !hasSignal(a, "DELETE_ROOT_PATH") {
			t.Errorf("bash shell analysis failed: %+v", a)
		}
		if a.Interpreter != "bash" {
			t.Errorf("Interpreter = %q, want bash", a.Interpreter)
		}
	})

	t.Run("empty interpreter uses shell analyzer", func(t *testing.T) {
		a := AnalyzeStep("rm -rf /", "")
		if !a.Critical {
			t.Errorf("default shell analysis failed: %+v", a)
		}
	})

	t.Run("python interpreter uses python analyzer", func(t *testing.T) {
		a := AnalyzeStep(`os.system("rm -rf /")`, "python3")
		if !a.Critical || !hasSignal(a, "DELETE_ROOT_PATH") {
			t.Errorf("python analysis failed: %+v", a)
		}
		if a.Interpreter != "python3" {
			t.Errorf("Interpreter = %q, want python3", a.Interpreter)
		}
	})

	t.Run("unknown interpreter is not parsed", func(t *testing.T) {
		a := AnalyzeStep(`require("fs").rmSync("/", {recursive: true})`, "node")
		if len(a.Signals) != 0 {
			t.Errorf("expected no signals for node, got %+v", a.Signals)
		}
		if a.Interpreter != "node" {
			t.Errorf("Interpreter = %q, want node", a.Interpreter)
		}
	})
}
