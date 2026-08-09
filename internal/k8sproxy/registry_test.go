package k8sproxy

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"
)

// TestMain is defined once for the package in proxy_test.go.

func twoClusterSpecs() []ClusterSpec {
	return []ClusterSpec{
		{
			Name:          "prod",
			Config:        &rest.Config{Host: "http://prod.example.com"},
			DefaultGroups: []string{"developers"},
		},
		{
			Name:          "staging",
			Config:        &rest.Config{Host: "http://staging.example.com"},
			DefaultGroups: []string{"developers", "staging-admins"},
		},
	}
}

func TestNewRegistry_Has(t *testing.T) {
	reg, err := NewRegistry(twoClusterSpecs())
	require.NoError(t, err)

	require.True(t, reg.Has("prod"))
	require.True(t, reg.Has("staging"))
	require.False(t, reg.Has("does-not-exist"))
}

func TestNewRegistry_IdentityFor(t *testing.T) {
	reg, err := NewRegistry(twoClusterSpecs())
	require.NoError(t, err)

	ident, ok := reg.IdentityFor("prod", "alice")
	require.True(t, ok)
	require.Equal(t, Identity{User: "alice", Groups: []string{"developers"}}, ident)

	ident, ok = reg.IdentityFor("staging", "bob")
	require.True(t, ok)
	require.Equal(t, Identity{User: "bob", Groups: []string{"developers", "staging-admins"}}, ident)

	_, ok = reg.IdentityFor("does-not-exist", "alice")
	require.False(t, ok)
}

func TestNewRegistry_DuplicateName(t *testing.T) {
	specs := []ClusterSpec{
		{Name: "prod", Config: &rest.Config{Host: "http://a.example.com"}},
		{Name: "prod", Config: &rest.Config{Host: "http://b.example.com"}},
	}
	_, err := NewRegistry(specs)
	require.Error(t, err)
	require.Contains(t, err.Error(), "prod")
}

func TestNewRegistry_EmptyName(t *testing.T) {
	specs := []ClusterSpec{
		{Name: "", Config: &rest.Config{Host: "http://a.example.com"}},
	}
	_, err := NewRegistry(specs)
	require.Error(t, err)
}

func TestNewRegistry_NilConfig(t *testing.T) {
	specs := []ClusterSpec{
		{Name: "prod", Config: nil},
	}
	_, err := NewRegistry(specs)
	require.Error(t, err)
	require.Contains(t, err.Error(), "prod")
}

func TestNewRegistry_EmptySpecsOK(t *testing.T) {
	reg, err := NewRegistry(nil)
	require.NoError(t, err)
	require.False(t, reg.Has("anything"))
}

func TestRegistry_Serve_UnknownClusterReturnsFalseNoPanic(t *testing.T) {
	reg, err := NewRegistry(twoClusterSpecs())
	require.NoError(t, err)

	r := newInboundRequest(t, "http://honey.local/does-not-exist/api/v1/namespaces/default/pods")
	w := httptest.NewRecorder()

	require.NotPanics(t, func() {
		ok := reg.Serve(w, r, "does-not-exist", Identity{User: "alice"})
		require.False(t, ok)
	})
}

func TestRegistry_Serve_KnownClusterForwards(t *testing.T) {
	srv := newEchoServer(t)
	defer srv.Close()

	reg, err := NewRegistry([]ClusterSpec{
		{Name: "prod", Config: &rest.Config{Host: srv.URL}, DefaultGroups: []string{"developers"}},
	})
	require.NoError(t, err)

	r := newInboundRequest(t, "http://honey.local/api/v1/namespaces/default/pods")
	w := httptest.NewRecorder()

	ok := reg.Serve(w, r, "prod", Identity{User: "alice", Groups: []string{"developers"}})
	require.True(t, ok)
	require.Equal(t, 418, w.Code) // http.StatusTeapot, from the shared echo server helper
}
