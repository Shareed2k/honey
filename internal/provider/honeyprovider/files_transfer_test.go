package honeyprovider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/shareed2k/honey/internal/devmtls"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/stretchr/testify/assert"
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

// TestClient_UploadDownload_Mesh covers the doRequestWithBody (Upload) and
// doDownload (Download) call sites. Swaps the package-level meshDial
// (white-box only); these subtests share that var so, unlike other subtests
// in this file, they are not run with t.Parallel().
func TestClient_UploadDownload_Mesh(t *testing.T) {
	uploadTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		res := map[string]any{"success": true}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	}))
	defer uploadTS.Close()

	downloadTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("test file content"))
	}))
	defer downloadTS.Close()

	t.Run("mesh true routes Upload through meshDial", func(t *testing.T) {
		orig := meshDial
		var gotAddr string
		meshDial = func(ctx context.Context, meshAddr string) (net.Conn, error) {
			gotAddr = meshAddr
			var d net.Dialer
			return d.DialContext(ctx, "tcp", uploadTS.Listener.Addr().String())
		}
		t.Cleanup(func() { meshDial = orig })

		c := &Client{url: "http://mesh-target.invalid", mesh: true, meshAddr: "/ip4/1.2.3.4/tcp/4001/p2p/target", user: "ubuntu", record: hosts.Record{Name: "test-host"}}

		dir := t.TempDir()
		localPath := filepath.Join(dir, "local.txt")
		require.NoError(t, os.WriteFile(localPath, []byte("test file content"), 0o644))

		err := c.Upload(localPath, "/remote/path.txt")
		require.NoError(t, err)
		assert.Equal(t, "/ip4/1.2.3.4/tcp/4001/p2p/target", gotAddr)
	})

	t.Run("mesh false uses the default dialer for Upload", func(t *testing.T) {
		orig := meshDial
		called := false
		meshDial = func(context.Context, string) (net.Conn, error) {
			called = true
			return nil, errors.New("meshDial should not be called")
		}
		t.Cleanup(func() { meshDial = orig })

		c := &Client{url: uploadTS.URL, user: "ubuntu", record: hosts.Record{Name: "test-host"}}

		dir := t.TempDir()
		localPath := filepath.Join(dir, "local.txt")
		require.NoError(t, os.WriteFile(localPath, []byte("test file content"), 0o644))

		err := c.Upload(localPath, "/remote/path.txt")
		require.NoError(t, err)
		assert.False(t, called)
	})

	t.Run("mesh true routes Download through meshDial", func(t *testing.T) {
		orig := meshDial
		var gotAddr string
		meshDial = func(ctx context.Context, meshAddr string) (net.Conn, error) {
			gotAddr = meshAddr
			var d net.Dialer
			return d.DialContext(ctx, "tcp", downloadTS.Listener.Addr().String())
		}
		t.Cleanup(func() { meshDial = orig })

		c := &Client{url: "http://mesh-target.invalid", mesh: true, meshAddr: "/ip4/1.2.3.4/tcp/4001/p2p/target", user: "ubuntu", record: hosts.Record{Name: "test-host"}}

		dir := t.TempDir()
		localPath := filepath.Join(dir, "local.txt")

		err := c.Download("/remote/path.txt", localPath)
		require.NoError(t, err)
		assert.Equal(t, "/ip4/1.2.3.4/tcp/4001/p2p/target", gotAddr)
	})

	t.Run("mesh false uses the default dialer for Download", func(t *testing.T) {
		orig := meshDial
		called := false
		meshDial = func(context.Context, string) (net.Conn, error) {
			called = true
			return nil, errors.New("meshDial should not be called")
		}
		t.Cleanup(func() { meshDial = orig })

		c := &Client{url: downloadTS.URL, user: "ubuntu", record: hosts.Record{Name: "test-host"}}

		dir := t.TempDir()
		localPath := filepath.Join(dir, "local.txt")

		err := c.Download("/remote/path.txt", localPath)
		require.NoError(t, err)
		assert.False(t, called)
	})
}

// fakeSigner is a devmtls.Signer stub; ClientTLSConfig fails before ever
// invoking Sign (it fails while parsing the bogus chain PEM below), so this
// never actually needs to produce a real signature.
type fakeSigner struct{}

func (fakeSigner) Sign([]byte) ([]byte, error) { return nil, nil }

// TestClient_UploadDownload_DoNotApplyMTLS locks in the brief's explicit
// behavior-preservation constraint: doRequestWithBody/doDownload must keep
// NOT applying mTLS even when c.mtls is true, despite now routing through
// the shared buildTransport helper (which, unlike their old inline TLS
// construction, does honor mtls when asked). We register a devmtls
// credential whose chain PEM is deliberately malformed, so
// devmtls.ClientTLSConfig always errors -- if either method regressed to
// actually pass mtls: true into buildTransport, its transport construction
// (and thus the whole request) would fail with that error. Because this
// mutates the process-wide devmtls registration, this test is intentionally
// not run with t.Parallel().
func TestClient_UploadDownload_DoNotApplyMTLS(t *testing.T) {
	devmtls.Set([]byte("not a valid certificate chain"), nil, fakeSigner{})
	t.Cleanup(devmtls.Clear)
	require.True(t, devmtls.Registered())

	t.Run("Upload", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			res := map[string]any{"success": true}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(res)
		}))
		defer ts.Close()

		c := &Client{url: ts.URL, mtls: true, user: "ubuntu", record: hosts.Record{Name: "test-host"}}

		dir := t.TempDir()
		localPath := filepath.Join(dir, "local.txt")
		require.NoError(t, os.WriteFile(localPath, []byte("data"), 0o644))

		err := c.Upload(localPath, "/remote/path.txt")
		require.NoError(t, err, "doRequestWithBody must not honor c.mtls -- if it did, the malformed devmtls credential above would surface as an error here")
	})

	t.Run("Download", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte("data"))
		}))
		defer ts.Close()

		c := &Client{url: ts.URL, mtls: true, user: "ubuntu", record: hosts.Record{Name: "test-host"}}

		dir := t.TempDir()
		localPath := filepath.Join(dir, "local.txt")

		err := c.Download("/remote/path.txt", localPath)
		require.NoError(t, err, "doDownload must not honor c.mtls -- if it did, the malformed devmtls credential above would surface as an error here")
	})
}
