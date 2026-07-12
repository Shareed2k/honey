package honeyprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutor_Dial(t *testing.T) {
	ex := &Executor{URL: "http://test", Token: "abc", Insecure: true, Mesh: true, MeshAddr: "/ip4/1.2.3.4/tcp/4001/p2p/target"}
	rec := hosts.Record{Name: "test-host"}

	client, err := ex.Dial("ubuntu", rec)
	require.NoError(t, err)
	require.NotNil(t, client)

	c, ok := client.(*Client)
	require.True(t, ok)
	require.Equal(t, "http://test", c.url)
	require.Equal(t, "abc", c.token)
	require.True(t, c.insecure)
	require.True(t, c.mesh)
	require.Equal(t, "/ip4/1.2.3.4/tcp/4001/p2p/target", c.meshAddr)
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

// TestClient_doRequest_Mesh covers the Client.doRequest call site (used by
// Run, ListRemoteDir, StatRemote, MkdirAllRemote, RemoveRemote). Swaps the
// package-level meshDial (white-box only); the two subtests share that var
// so, unlike other subtests in this file, they are not run with t.Parallel().
func TestClient_doRequest_Mesh(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		res := map[string]any{
			"results": []engine.HostExecResult{{Output: "ok", ExitCode: 0}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	}))
	defer ts.Close()

	t.Run("mesh true routes through meshDial", func(t *testing.T) {
		orig := meshDial
		var gotAddr string
		meshDial = func(ctx context.Context, meshAddr string) (net.Conn, error) {
			gotAddr = meshAddr
			var d net.Dialer
			return d.DialContext(ctx, "tcp", ts.Listener.Addr().String())
		}
		t.Cleanup(func() { meshDial = orig })

		c := &Client{url: "http://mesh-target.invalid", mesh: true, meshAddr: "/ip4/1.2.3.4/tcp/4001/p2p/target", user: "ubuntu", record: hosts.Record{Name: "test-host"}}
		_, err := c.Run("echo hi")
		require.NoError(t, err)
		assert.Equal(t, "/ip4/1.2.3.4/tcp/4001/p2p/target", gotAddr)
	})

	t.Run("mesh false uses the default dialer", func(t *testing.T) {
		orig := meshDial
		called := false
		meshDial = func(context.Context, string) (net.Conn, error) {
			called = true
			return nil, errors.New("meshDial should not be called")
		}
		t.Cleanup(func() { meshDial = orig })

		c := &Client{url: ts.URL, user: "ubuntu", record: hosts.Record{Name: "test-host"}}
		_, err := c.Run("echo hi")
		require.NoError(t, err)
		assert.False(t, called)
	})
}

// TestExecutor_DialUpstream_Mesh covers the Executor.DialUpstream call site
// (dialWS + meshDialContext over a websocket). Same meshDial-sharing caveat
// as above applies.
func TestExecutor_DialUpstream_Mesh(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var hello map[string]any
		if err := conn.ReadJSON(&hello); err != nil {
			return
		}
		_ = conn.WriteJSON(map[string]any{})
	}))
	defer ts.Close()

	t.Run("mesh true routes through meshDial", func(t *testing.T) {
		orig := meshDial
		var gotAddr string
		meshDial = func(ctx context.Context, meshAddr string) (net.Conn, error) {
			gotAddr = meshAddr
			var d net.Dialer
			return d.DialContext(ctx, "tcp", ts.Listener.Addr().String())
		}
		t.Cleanup(func() { meshDial = orig })

		e := &Executor{URL: "http://mesh-target.invalid", Mesh: true, MeshAddr: "/ip4/1.2.3.4/tcp/4001/p2p/relay/p2p-circuit/p2p/target"}
		conn, err := e.DialUpstream(context.Background(), "ubuntu", hosts.Record{Name: "test-host"}, "10.0.0.1:22")
		require.NoError(t, err)
		require.NotNil(t, conn)
		conn.Close()
		assert.Equal(t, "/ip4/1.2.3.4/tcp/4001/p2p/relay/p2p-circuit/p2p/target", gotAddr)
	})

	t.Run("mesh false uses the default dialer", func(t *testing.T) {
		orig := meshDial
		called := false
		meshDial = func(context.Context, string) (net.Conn, error) {
			called = true
			return nil, errors.New("meshDial should not be called")
		}
		t.Cleanup(func() { meshDial = orig })

		e := &Executor{URL: ts.URL}
		conn, err := e.DialUpstream(context.Background(), "ubuntu", hosts.Record{Name: "test-host"}, "10.0.0.1:22")
		require.NoError(t, err)
		require.NotNil(t, conn)
		conn.Close()
		assert.False(t, called)
	})
}

// TestClient_RunWithStreams_Mesh covers the Client.RunWithStreams call site
// (dialWS + meshDialContext over a websocket). Same meshDial-sharing caveat
// as above applies.
func TestClient_RunWithStreams_Mesh(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var hello map[string]any
		if err := conn.ReadJSON(&hello); err != nil {
			return
		}
		_ = conn.WriteJSON(map[string]any{})
		_ = conn.WriteMessage(websocket.TextMessage, []byte("done"))
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	}))
	defer ts.Close()

	t.Run("mesh true routes through meshDial", func(t *testing.T) {
		orig := meshDial
		var gotAddr string
		meshDial = func(ctx context.Context, meshAddr string) (net.Conn, error) {
			gotAddr = meshAddr
			var d net.Dialer
			return d.DialContext(ctx, "tcp", ts.Listener.Addr().String())
		}
		t.Cleanup(func() { meshDial = orig })

		c := &Client{url: "http://mesh-target.invalid", mesh: true, meshAddr: "/ip4/1.2.3.4/tcp/4001/p2p/target", user: "ubuntu", record: hosts.Record{Name: "test-host"}}
		var out bytes.Buffer
		err := c.RunWithStreams("echo hi", nil, &out, nil)
		require.NoError(t, err)
		assert.Equal(t, "/ip4/1.2.3.4/tcp/4001/p2p/target", gotAddr)
	})

	t.Run("mesh false uses the default dialer", func(t *testing.T) {
		orig := meshDial
		called := false
		meshDial = func(context.Context, string) (net.Conn, error) {
			called = true
			return nil, errors.New("meshDial should not be called")
		}
		t.Cleanup(func() { meshDial = orig })

		c := &Client{url: ts.URL, user: "ubuntu", record: hosts.Record{Name: "test-host"}}
		var out bytes.Buffer
		err := c.RunWithStreams("echo hi", nil, &out, nil)
		require.NoError(t, err)
		assert.False(t, called)
	})
}
