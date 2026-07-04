package honeyprovider

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/stretchr/testify/require"
)

func TestClient_Upload(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/files/remote/upload", r.URL.Path)
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "ubuntu", r.URL.Query().Get("ssh_user"))
		require.Equal(t, "/remote/path.txt", r.URL.Query().Get("path"))

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, "test file content", string(body))

		res := map[string]any{"success": true}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	}))
	defer ts.Close()

	c := &Client{
		url:    ts.URL,
		user:   "ubuntu",
		record: hosts.Record{Name: "test-host"},
	}

	dir := t.TempDir()
	localPath := filepath.Join(dir, "local.txt")
	require.NoError(t, os.WriteFile(localPath, []byte("test file content"), 0o644))

	err := c.Upload(localPath, "/remote/path.txt")
	require.NoError(t, err)
}

func TestClient_Download(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/files/remote/download", r.URL.Path)
		require.Equal(t, "GET", r.Method)
		require.Equal(t, "ubuntu", r.URL.Query().Get("ssh_user"))
		require.Equal(t, "/remote/path.txt", r.URL.Query().Get("path"))

		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("test file content"))
	}))
	defer ts.Close()

	c := &Client{
		url:    ts.URL,
		user:   "ubuntu",
		record: hosts.Record{Name: "test-host"},
	}

	dir := t.TempDir()
	localPath := filepath.Join(dir, "local.txt")

	err := c.Download("/remote/path.txt", localPath)
	require.NoError(t, err)

	content, err := os.ReadFile(localPath)
	require.NoError(t, err)
	require.Equal(t, "test file content", string(content))
}
