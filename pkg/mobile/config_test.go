//go:build ignore

package mobile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultSSHUser(t *testing.T) {
	const (
		sentinelEmptyPath   = "\x00empty"
		sentinelMissingFile = "\x00missing"
	)
	tests := []struct {
		name string
		yaml string
		path string // sentinel overrides; otherwise yaml is written to a temp file
		want string
	}{
		{name: "set", yaml: "version: 1\ndefaults:\n  ssh_user: ubuntu\n", want: "ubuntu"},
		{name: "unset", yaml: "version: 1\ndefaults: {}\n", want: ""},
		{name: "whitespace trimmed", yaml: "version: 1\ndefaults:\n  ssh_user: \"  ec2-user  \"\n", want: "ec2-user"},
		{name: "empty path", path: sentinelEmptyPath, want: ""},
		{name: "missing file", path: sentinelMissingFile, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p string
			switch tt.path {
			case sentinelEmptyPath:
				p = ""
			case sentinelMissingFile:
				p = filepath.Join(t.TempDir(), "nope.yaml")
			default:
				p = filepath.Join(t.TempDir(), "config.yaml")
				if err := os.WriteFile(p, []byte(tt.yaml), 0o600); err != nil {
					t.Fatalf("write config: %v", err)
				}
			}
			if got := defaultSSHUser(p); got != tt.want {
				t.Errorf("defaultSSHUser = %q, want %q", got, tt.want)
			}
		})
	}
}
