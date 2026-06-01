package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildScriptInvocationCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		path        string
		interpreter string
		quoted      bool
		want        string
	}{
		{name: "direct", path: "/tmp/a.sh", want: "'/tmp/a.sh'"},
		{name: "interpreter", path: "/tmp/a.sh", interpreter: "bash", want: "bash '/tmp/a.sh'"},
		{name: "placeholder", path: "/tmp/a.sh", interpreter: "sudo -u ops ${scriptfile}", want: "sudo -u ops '/tmp/a.sh'"},
		{name: "quoted", path: "/tmp/a b.sh", interpreter: "bash -lc", quoted: true, want: "bash -lc '/tmp/a b.sh'"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildScriptInvocationCommand(tc.path, tc.interpreter, tc.quoted)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizeScriptFileExtension(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", ".sh", false},
		{"sh", ".sh", false},
		{".bash", ".bash", false},
		{"../x", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := normalizeScriptFileExtension(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestPrepareScriptContentFile(t *testing.T) {
	t.Parallel()
	localAbs, remotePath, cleanup, err := prepareScriptContentFile("echo hi\n", "bash")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err := os.Stat(localAbs); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(localAbs, ".bash") {
		t.Fatalf("local path %q should use .bash extension", localAbs)
	}
	if filepath.Base(remotePath) != filepath.Base(localAbs) {
		t.Fatalf("remote path %q should use local base %q", remotePath, filepath.Base(localAbs))
	}
}
