package honeyprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Note: these tests swap the package-level meshDial variable, so (per the
// task-4 brief's own admission that this is shared, mutable package state)
// they intentionally do NOT call t.Parallel() -- doing so would race with
// any other test in this package that also swaps meshDial.

func TestMeshDialContext(t *testing.T) {
	t.Run("false returns nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, meshDialContext(false, "/ip4/1.2.3.4/tcp/4001/p2p/test"))
	})

	t.Run("true returns a dialer that calls meshDial with meshAddr", func(t *testing.T) {
		orig := meshDial
		var gotAddr string
		meshDial = func(_ context.Context, meshAddr string) (net.Conn, error) {
			gotAddr = meshAddr
			return nil, errors.New("stub")
		}
		t.Cleanup(func() { meshDial = orig })

		dialFn := meshDialContext(true, "/ip4/1.2.3.4/tcp/4001/p2p/test")
		require.NotNil(t, dialFn)

		_, err := dialFn(context.Background(), "tcp", "ignored:0")
		require.Error(t, err)
		assert.Equal(t, "/ip4/1.2.3.4/tcp/4001/p2p/test", gotAddr)
	})
}

func TestBuildTransport(t *testing.T) {
	t.Run("mesh false leaves DialContext nil", func(t *testing.T) {
		t.Parallel()
		tr, err := buildTransport(trustConfig{insecure: true})
		require.NoError(t, err)
		assert.Nil(t, tr.DialContext)
		require.NotNil(t, tr.TLSClientConfig)
		assert.True(t, tr.TLSClientConfig.InsecureSkipVerify)
	})

	t.Run("mesh true sets DialContext routed through meshDial", func(t *testing.T) {
		orig := meshDial
		var gotAddr string
		meshDial = func(_ context.Context, meshAddr string) (net.Conn, error) {
			gotAddr = meshAddr
			return nil, errors.New("stub")
		}
		t.Cleanup(func() { meshDial = orig })

		tr, err := buildTransport(trustConfig{mesh: true, meshAddr: "/ip4/9.9.9.9/tcp/1/p2p/x"})
		require.NoError(t, err)
		require.NotNil(t, tr.DialContext)

		_, dialErr := tr.DialContext(context.Background(), "tcp", "unused:0")
		require.Error(t, dialErr)
		assert.Equal(t, "/ip4/9.9.9.9/tcp/1/p2p/x", gotAddr)
	})

	t.Run("mtls error propagates (no device credential registered)", func(t *testing.T) {
		t.Parallel()
		_, err := buildTransport(trustConfig{mtls: true})
		require.Error(t, err)
	})
}

func TestBuildWSDialer(t *testing.T) {
	t.Run("mesh false leaves NetDialContext nil", func(t *testing.T) {
		t.Parallel()
		d, err := buildWSDialer(trustConfig{insecure: true})
		require.NoError(t, err)
		assert.Nil(t, d.NetDialContext)
		assert.Equal(t, 15*time.Second, d.HandshakeTimeout)
		require.NotNil(t, d.TLSClientConfig)
		assert.True(t, d.TLSClientConfig.InsecureSkipVerify)
	})

	t.Run("mesh true sets NetDialContext routed through meshDial", func(t *testing.T) {
		orig := meshDial
		var gotAddr string
		meshDial = func(_ context.Context, meshAddr string) (net.Conn, error) {
			gotAddr = meshAddr
			return nil, errors.New("stub")
		}
		t.Cleanup(func() { meshDial = orig })

		d, err := buildWSDialer(trustConfig{mesh: true, meshAddr: "/ip4/9.9.9.9/tcp/1/p2p/x"})
		require.NoError(t, err)
		require.NotNil(t, d.NetDialContext)

		_, dialErr := d.NetDialContext(context.Background(), "tcp", "unused:0")
		require.Error(t, dialErr)
		assert.Equal(t, "/ip4/9.9.9.9/tcp/1/p2p/x", gotAddr)
	})
}

// mockCfgProvider is a minimal ConfigProvider for exercising honeyFactory
// directly (white-box, so it can reach the unexported honeyFactory type and
// swap meshDial -- factory_test.go, by contrast, is package
// honeyprovider_test and cannot do either).
type mockCfgProvider struct {
	backends []config.HoneyBackend
}

func (m *mockCfgProvider) HoneyBackends() []config.HoneyBackend         { return m.backends }
func (m *mockCfgProvider) HoneyBackendSlicePtr() *[]config.HoneyBackend { return &m.backends }
func (m *mockCfgProvider) SetHoneyBackends(b []config.HoneyBackend)     { m.backends = b }

// TestBackendRows_MeshRouting covers the 7th call site: honeyFactory.BackendRows'
// per-backend fetch client, which independently builds its own transport.
func TestBackendRows_MeshRouting(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/backends", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(hostapi.ListBackendsOutput{
			Backends: []config.BackendRow{{Kind: "ssh", Name: "remote", Hint: "10.0.0.1"}},
		})
	}))
	defer ts.Close()

	t.Run("mesh true routes the backend fetch through meshDial", func(t *testing.T) {
		orig := meshDial
		var gotAddr string
		meshDial = func(ctx context.Context, meshAddr string) (net.Conn, error) {
			gotAddr = meshAddr
			var d net.Dialer
			return d.DialContext(ctx, "tcp", ts.Listener.Addr().String())
		}
		t.Cleanup(func() { meshDial = orig })

		f := honeyFactory{cfg: &mockCfgProvider{backends: []config.HoneyBackend{
			{Name: "mesh-honey", URL: "http://mesh-target.invalid", Mesh: true, MeshAddr: "/ip4/1.2.3.4/tcp/4001/p2p/target"},
		}}}

		rows := f.BackendRows()
		var sshCount int
		for _, r := range rows {
			if r.Kind == "ssh" {
				sshCount++
			}
		}
		assert.Equal(t, 1, sshCount, "expected the mesh-routed fetch to succeed and return the ssh row")
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

		f := honeyFactory{cfg: &mockCfgProvider{backends: []config.HoneyBackend{
			{Name: "plain-honey", URL: ts.URL},
		}}}

		rows := f.BackendRows()
		var sshCount int
		for _, r := range rows {
			if r.Kind == "ssh" {
				sshCount++
			}
		}
		assert.Equal(t, 1, sshCount)
		assert.False(t, called)
	})
}
