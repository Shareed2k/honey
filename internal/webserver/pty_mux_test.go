package webserver

import "testing"

func TestTmuxPaneRunning(t *testing.T) {
	t.Parallel()
	if !tmuxPaneRunning("0", "12345", "/tmp/honey pty-proxy") {
		t.Fatal("expected running pane")
	}
	if tmuxPaneRunning("0", "", "/tmp/honey") {
		t.Fatal("empty pid should not run")
	}
	if tmuxPaneRunning("0", "1", "[exited]") {
		t.Fatal("[exited] should not run")
	}
}

func TestTmuxSessionAlive_exitedPane(t *testing.T) {
	t.Parallel()
	if tmuxSessionAlive("honey_nonexistent_test_session_12345") {
		t.Fatal("expected false for missing session")
	}
}

func TestTmuxSessionFullyExited_missing(t *testing.T) {
	if tmuxSessionFullyExited("honey_nonexistent_test_session_12345") {
		t.Fatal("expected false for missing session")
	}
}

func TestZellijSessionAlive_missingSession(t *testing.T) {
	if zellijSessionAlive("honey_nonexistent_test_session_12345") {
		t.Fatal("expected false for missing session")
	}
}
