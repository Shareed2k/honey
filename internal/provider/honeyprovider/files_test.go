package honeyprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/stretchr/testify/require"
)

func TestClient_StatRemote(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/files/remote/stat", r.URL.Path)

		res := map[string]any{
			"entry": hostexec.RemoteFileEntry{
				Name:  "file1.txt",
				Path:  "/tmp/file1.txt",
				IsDir: false,
				Size:  100,
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

	entry, err := c.StatRemote("/tmp/file1.txt")
	require.NoError(t, err)
	require.Equal(t, "file1.txt", entry.Name)
	require.False(t, entry.IsDir)
	require.Equal(t, int64(100), entry.Size)
}

func TestClient_MkdirAllRemote(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/files/remote/mkdir", r.URL.Path)
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

	err := c.MkdirAllRemote("/tmp/newdir")
	require.NoError(t, err)
}

func TestClient_RemoveRemote(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/files/remote/remove", r.URL.Path)
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

	err := c.RemoveRemote("/tmp/file1.txt", false)
	require.NoError(t, err)
}
