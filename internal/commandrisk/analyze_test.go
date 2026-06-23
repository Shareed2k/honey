package commandrisk

import (
	"slices"
	"testing"
)

func TestAnalyze(t *testing.T) {
	cases := []struct {
		name       string
		command    string
		wantSignal string // expected signal ID ("" = none)
		wantSev    Severity
		wantCrit   bool
	}{
		{"safe uptime", "uptime", "", "", false},
		{"safe df", "df -h", "", "", false},
		{"safe kubectl get", "kubectl get pods -n prod", "", "", false},
		{"rm recursive", "rm -rf /tmp/foo", "RM_RECURSIVE_FORCE", SeverityHigh, false},
		{"rm root", "rm -rf /", "DELETE_ROOT_PATH", SeverityCritical, true},
		{"rm root star", "rm -rf /*", "DELETE_ROOT_PATH", SeverityCritical, true},
		{"rm unguarded var", `rm -rf "$DIR"`, "UNRESOLVED_VARIABLE_IN_PATH", SeverityCritical, true},
		{"rm guarded var ok", `rm -rf "${DIR:?}"`, "RM_RECURSIVE_FORCE", SeverityHigh, false},
		{"curl pipe sh", "curl https://x | sh", "CURL_PIPE_SHELL", SeverityCritical, true},
		{"wget pipe bash", "wget -qO- https://x | bash", "CURL_PIPE_SHELL", SeverityCritical, true},
		{"eval curl subst", `eval "$(curl https://x)"`, "REMOTE_DOWNLOAD_EXECUTE", SeverityCritical, true},
		{"dd block device", "dd if=/dev/zero of=/dev/sda", "DD_WRITE_BLOCK_DEVICE", SeverityCritical, true},
		{"mkfs", "mkfs.ext4 /dev/sdb1", "MKFS_FILESYSTEM", SeverityCritical, true},
		{"chmod recursive root", "chmod -R 777 /", "CHMOD_RECURSIVE_SYSTEM_PATH", SeverityCritical, true},
		{"sudo", "sudo systemctl restart nginx", "SUDO_PRIVILEGE_ESCALATION", SeverityHigh, false},
		{"systemctl stop", "systemctl stop nginx", "SYSTEMCTL_STOP_SERVICE", SeverityHigh, false},
		{"kubectl delete", "kubectl delete pod x -n prod", "KUBECTL_DELETE", SeverityHigh, false},
		{"helm uninstall", "helm uninstall app -n prod", "HELM_UNINSTALL", SeverityHigh, false},
		{"docker prune", "docker system prune -a", "DOCKER_SYSTEM_PRUNE", SeverityHigh, false},
		{"aws s3 rm recursive", "aws s3 rm s3://b --recursive", "AWS_S3_RM_RECURSIVE", SeverityHigh, false},
		{"apt remove", "apt-get remove -y nginx", "PACKAGE_REMOVE", SeverityMedium, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := Analyze(tc.command)
			if tc.wantSignal == "" {
				if len(a.Signals) != 0 {
					t.Fatalf("expected no signals, got %+v", a.Signals)
				}
				return
			}
			if !hasSignal(a, tc.wantSignal) {
				t.Fatalf("expected signal %q, got %+v", tc.wantSignal, a.Signals)
			}
			if a.MaxSeverity != tc.wantSev {
				t.Fatalf("max severity = %q, want %q (signals %+v)", a.MaxSeverity, tc.wantSev, a.Signals)
			}
			if a.Critical != tc.wantCrit {
				t.Fatalf("critical = %v, want %v", a.Critical, tc.wantCrit)
			}
		})
	}
}

func TestAnalyze_ForkBomb(t *testing.T) {
	a := Analyze(":(){ :|:& };:")
	if !a.Critical || !hasSignal(a, "FORK_BOMB") {
		t.Fatalf("fork bomb not detected: %+v", a.Signals)
	}
}

func TestAnalyze_Unparseable(t *testing.T) {
	a := Analyze("if then fi (((")
	if a.ParseError == "" || !hasSignal(a, "UNPARSEABLE_COMMAND") {
		t.Fatalf("expected parse error signal, got %+v", a)
	}
}

func TestAnalyze_Detected(t *testing.T) {
	a := Analyze("rm -rf /tmp/foo")
	if !slices.Contains(a.Detected.Commands, "rm") {
		t.Fatalf("commands = %v, want rm", a.Detected.Commands)
	}
	if !slices.Contains(a.Detected.Paths, "/tmp/foo") {
		t.Fatalf("paths = %v, want /tmp/foo", a.Detected.Paths)
	}
}

func hasSignal(a Analysis, id string) bool {
	for _, s := range a.Signals {
		if s.ID == id {
			return true
		}
	}
	return false
}
