package honeyprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/stretchr/testify/require"
)

func TestExecutor_Dial(t *testing.T) {
	ex := &Executor{URL: "http://test", Token: "abc", Insecure: true}
	rec := hosts.Record{Name: "test-host"}

	client, err := ex.Dial("ubuntu", rec)
	require.NoError(t, err)
	require.NotNil(t, client)

	c, ok := client.(*Client)
	require.True(t, ok)
	require.Equal(t, "http://test", c.url)
	require.Equal(t, "abc", c.token)
	require.True(t, c.insecure)
	require.Equal(t, "ubuntu", c.user)
	require.Equal(t, "test-host", c.record.Name)
}

func TestClient_Run(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/exec", r.URL.Path)
		require.Equal(t, "Bearer abc", r.Header.Get("Authorization"))

		res := map[string]any{
			"results": []engine.HostExecResult{
				{Output: "test output", ExitCode: 0},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	}))
	defer ts.Close()

	c := &Client{
		url:    ts.URL,
		token:  "abc",
		user:   "ubuntu",
		record: hosts.Record{Name: "test-host"},
	}

	out, err := c.Run("echo hello")
	require.NoError(t, err)
	require.Equal(t, "test output", string(out))
}

func TestClient_Run_Error(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		res := map[string]any{
			"results": []engine.HostExecResult{
				{Output: "test error output", ExitCode: 1, ErrMsg: "command failed"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	}))
	defer ts.Close()

	c := &Client{
		url:    ts.URL,
		user:   "ubuntu",
		record: hosts.Record{Name: "test-host"},
	}

	out, err := c.Run("false")
	require.Error(t, err)
	require.Contains(t, err.Error(), "command failed")
	require.Equal(t, "test error output", string(out))
}

func TestClient_ListRemoteDir(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/files/remote/list", r.URL.Path)

		res := map[string]any{
			"path": "/tmp",
			"entries": []hostexec.RemoteFileEntry{
				{Name: "file1.txt", Path: "/tmp/file1.txt", IsDir: false, Size: 100},
				{Name: "dir1", Path: "/tmp/dir1", IsDir: true, Size: 4096},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	}))
	defer ts.Close()

	c := &Client{
		url:    ts.URL,
		user:   "ubuntu",
		record: hosts.Record{Name: "test-host"},
	}

	entries, err := c.ListRemoteDir("/tmp")
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.Equal(t, "file1.txt", entries[0].Name)
	require.False(t, entries[0].IsDir)
	require.Equal(t, int64(100), entries[0].Size)
	require.Equal(t, "dir1", entries[1].Name)
	require.True(t, entries[1].IsDir)
}
