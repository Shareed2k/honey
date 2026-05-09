package transferagent

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestExpandAgentDownloadTemplate(t *testing.T) {
	t.Parallel()
	got := expandAgentDownloadTemplate("https://x/{os}-{arch}-{GOOS}-{GOARCH}", "linux", "arm64")
	want := "https://x/linux-arm64-linux-arm64"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFetchAgentBinary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dest := filepath.Join(dir, "agent")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bin" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("#!/bin/sh\necho ok\n"))
	}))
	t.Cleanup(srv.Close)

	if err := fetchAgentBinary(srv.URL+"/bin", dest); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() < 10 {
		t.Fatalf("unexpected size %d", st.Size())
	}
}

func TestTransferAgentDownloadURL_default(t *testing.T) {
	t.Setenv(agentDownloadURLEnv, "")
	t.Setenv(agentDownloadBaseEnv, "")
	t.Setenv(agentDownloadDisableDefaultEnv, "")
	SetEmbeddedHoneyVersion("")
	t.Cleanup(func() { SetEmbeddedHoneyVersion("") })
	u, ok := transferAgentDownloadURL("linux", "amd64")
	if !ok {
		t.Fatal("expected default URL when env unset")
	}
	want := "https://github.com/shareed2k/honey/releases/latest/download/honey-transfer-agent-linux-amd64"
	if u != want {
		t.Fatalf("got %q want %q", u, want)
	}
}

func TestTransferAgentDownloadURL_embeddedVersion(t *testing.T) {
	t.Setenv(agentDownloadURLEnv, "")
	t.Setenv(agentDownloadBaseEnv, "")
	t.Setenv(agentDownloadDisableDefaultEnv, "")
	SetEmbeddedHoneyVersion("2.1.0")
	t.Cleanup(func() { SetEmbeddedHoneyVersion("") })
	u, ok := transferAgentDownloadURL("linux", "amd64")
	if !ok {
		t.Fatal("expected URL")
	}
	want := "https://github.com/shareed2k/honey/releases/download/v2.1.0/honey-transfer-agent-linux-amd64"
	if u != want {
		t.Fatalf("got %q want %q", u, want)
	}
}

func TestTransferAgentDownloadURL_embeddedVersionAlreadyPrefixed(t *testing.T) {
	t.Setenv(agentDownloadURLEnv, "")
	t.Setenv(agentDownloadBaseEnv, "")
	t.Setenv(agentDownloadDisableDefaultEnv, "")
	SetEmbeddedHoneyVersion("v3.0.0-rc1")
	t.Cleanup(func() { SetEmbeddedHoneyVersion("") })
	u, ok := transferAgentDownloadURL("darwin", "arm64")
	if !ok {
		t.Fatal("expected URL")
	}
	want := "https://github.com/shareed2k/honey/releases/download/v3.0.0-rc1/honey-transfer-agent-darwin-arm64"
	if u != want {
		t.Fatalf("got %q want %q", u, want)
	}
}

func TestTransferAgentDownloadURL_disableDefault(t *testing.T) {
	t.Setenv(agentDownloadURLEnv, "")
	t.Setenv(agentDownloadBaseEnv, "")
	t.Setenv(agentDownloadDisableDefaultEnv, "1")
	SetEmbeddedHoneyVersion("1.0.0")
	t.Cleanup(func() { SetEmbeddedHoneyVersion("") })
	if _, ok := transferAgentDownloadURL("linux", "amd64"); ok {
		t.Fatal("expected no URL when default disabled")
	}
}

func TestTransferAgentDownloadURL_devVersionUsesLatest(t *testing.T) {
	t.Setenv(agentDownloadURLEnv, "")
	t.Setenv(agentDownloadBaseEnv, "")
	t.Setenv(agentDownloadDisableDefaultEnv, "")
	SetEmbeddedHoneyVersion("0.0.0-dev")
	t.Cleanup(func() { SetEmbeddedHoneyVersion("") })
	u, ok := transferAgentDownloadURL("linux", "arm64")
	if !ok {
		t.Fatal("expected URL")
	}
	want := "https://github.com/shareed2k/honey/releases/latest/download/honey-transfer-agent-linux-arm64"
	if u != want {
		t.Fatalf("got %q want %q", u, want)
	}
}

func TestTransferAgentDownloadURL_base(t *testing.T) {
	t.Setenv(agentDownloadURLEnv, "")
	t.Setenv(agentDownloadDisableDefaultEnv, "")
	SetEmbeddedHoneyVersion("")
	t.Cleanup(func() { SetEmbeddedHoneyVersion("") })
	t.Setenv(agentDownloadBaseEnv, "https://example.com/rel")
	u, ok := transferAgentDownloadURL("darwin", "arm64")
	if !ok {
		t.Fatal("expected URL with base env")
	}
	want := "https://example.com/rel/honey-transfer-agent-darwin-arm64"
	if u != want {
		t.Fatalf("got %q want %q", u, want)
	}
}

func TestTransferAgentDownloadURL_templateWins(t *testing.T) {
	t.Setenv(agentDownloadDisableDefaultEnv, "")
	SetEmbeddedHoneyVersion("")
	t.Cleanup(func() { SetEmbeddedHoneyVersion("") })
	t.Setenv(agentDownloadURLEnv, "https://cdn/{os}/{arch}/a")
	t.Setenv(agentDownloadBaseEnv, "https://ignored")
	u, ok := transferAgentDownloadURL("linux", "amd64")
	if !ok {
		t.Fatal("expected URL with template env")
	}
	if want := "https://cdn/linux/amd64/a"; u != want {
		t.Fatalf("got %q want %q", u, want)
	}
}
