package transferagent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepoRootFromSource(t *testing.T) {
	t.Parallel()
	root, err := repoRootFromSource()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root %q: missing go.mod: %v", root, err)
	}
	agentDir := filepath.Join(root, "cmd", "honey-transfer-agent")
	st, err := os.Stat(agentDir)
	if err != nil || !st.IsDir() {
		t.Fatalf("repo root %q: missing cmd/honey-transfer-agent: %v", root, err)
	}
}

func TestTransferAgentSourceStamp(t *testing.T) {
	t.Parallel()
	root, err := repoRootFromSource()
	if err != nil {
		t.Fatal(err)
	}
	stamp, err := transferAgentSourceStamp(root)
	if err != nil {
		t.Fatal(err)
	}
	if stamp.IsZero() {
		t.Fatal("expected non-zero source stamp")
	}
}
