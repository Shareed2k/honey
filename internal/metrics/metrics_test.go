package metrics

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockHijacker implements http.ResponseWriter and http.Hijacker
type mockHijacker struct {
	http.ResponseWriter
	hijacked bool
}

func (m *mockHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	m.hijacked = true
	// Returning an error just to avoid having to mock net.Conn
	return nil, nil, fmt.Errorf("mock hijack")
}

// mockFlusher implements http.ResponseWriter and http.Flusher
type mockFlusher struct {
	http.ResponseWriter
	flushed bool
}

func (m *mockFlusher) Flush() {
	m.flushed = true
}

func TestRegistry_Middleware(t *testing.T) {
	reg := NewRegistry("1.0.0", "abc1234")
	require.NotNil(t, reg)

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("ok"))
	})

	middleware := reg.Middleware(handler)

	req := httptest.NewRequest(http.MethodGet, "/test/route", nil)
	rec := httptest.NewRecorder()

	middleware.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "ok", rec.Body.String())

	// Test the statusRecorder's extra methods
	t.Run("Hijack", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test/hijack", nil)
		rec := httptest.NewRecorder()
		hijacker := &mockHijacker{ResponseWriter: rec}

		var statusRec *statusRecorder
		handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			statusRec = w.(*statusRecorder)
		})

		middleware := reg.Middleware(handler)
		middleware.ServeHTTP(hijacker, req)

		require.NotNil(t, statusRec)
		_, _, err := statusRec.Hijack()
		assert.Error(t, err)
		assert.True(t, hijacker.hijacked)
	})

	t.Run("Hijack_Failure", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test/hijack", nil)
		rec := httptest.NewRecorder() // not a hijacker

		var statusRec *statusRecorder
		handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			statusRec = w.(*statusRecorder)
		})

		middleware := reg.Middleware(handler)
		middleware.ServeHTTP(rec, req)

		require.NotNil(t, statusRec)
		_, _, err := statusRec.Hijack()
		assert.ErrorContains(t, err, "not an http.Hijacker")
	})

	t.Run("Flush", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test/flush", nil)
		rec := httptest.NewRecorder()
		flusher := &mockFlusher{ResponseWriter: rec}

		var statusRec *statusRecorder
		handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			statusRec = w.(*statusRecorder)
		})

		middleware := reg.Middleware(handler)
		middleware.ServeHTTP(flusher, req)

		require.NotNil(t, statusRec)
		statusRec.Flush()
		assert.True(t, flusher.flushed)
	})
}

func TestRegistry_Handler(t *testing.T) {
	reg := NewRegistry("1.0.0", "abc1234")
	handler := reg.Handler()
	require.NotNil(t, handler)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "honey_build_info")
}

func TestRegistry_Methods(t *testing.T) {
	reg := NewRegistry("1.0.0", "abc1234")

	t.Run("ObserveSearch", func(_ *testing.T) {
		reg.ObserveSearch(nil, time.Second, 10)
		reg.ObserveSearch(fmt.Errorf("error"), time.Second, 0)
	})

	t.Run("WebSocket", func(_ *testing.T) {
		reg.IncWS("test")
		reg.DecWS("test")
	})

	t.Run("ObserveRecipeRun", func(_ *testing.T) {
		reg.ObserveRecipeRun("dryrun", "cue", "ok", time.Second)
	})

	t.Run("ObserveRecipeStep", func(_ *testing.T) {
		reg.ObserveRecipeStep("bash", "ok", time.Second, 1)
		reg.ObserveRecipeStep("bash", "ok", time.Second, 3) // retry attempts > 1
	})

	t.Run("ObserveRecipeHostResult", func(_ *testing.T) {
		reg.ObserveRecipeHostResult("ok")
	})

	t.Run("ObservePluginExec", func(_ *testing.T) {
		reg.ObservePluginExec("sqlite", "query", "ok", time.Second)
		reg.ObservePluginExec("sqlite", "query", "ok", -1) // d < 0
	})

	t.Run("ObservePluginExecDuration", func(_ *testing.T) {
		reg.ObservePluginExecDuration("sqlite", "query", time.Second)
	})

	t.Run("ObserveSSHOperation", func(_ *testing.T) {
		reg.ObserveSSHOperation("exec", "ok", time.Second)
	})

	t.Run("ObserveAgentTransfer", func(_ *testing.T) {
		reg.ObserveAgentTransfer("ok", time.Second)
	})

	t.Run("ObserveRecipeValidate", func(_ *testing.T) {
		reg.ObserveRecipeValidate("ok", time.Second)
	})

	t.Run("ObserveExecCommand", func(_ *testing.T) {
		reg.ObserveExecCommand("ok", 5, time.Second)
	})

	// Just fetch metrics to ensure no panic during collection
	handler := reg.Handler()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestObserverEnabled(t *testing.T) {
	t.Run("nil interface", func(t *testing.T) {
		assert.False(t, ObserverEnabled(nil))
	})

	t.Run("nil registry", func(t *testing.T) {
		var reg *Registry
		assert.False(t, ObserverEnabled(reg))
	})

	t.Run("valid registry", func(t *testing.T) {
		reg := NewRegistry("1.0.0", "abc1234")
		assert.True(t, ObserverEnabled(reg))
	})
}

func TestNewRegistry_EmptyVersionCommit(t *testing.T) {
	reg := NewRegistry("", "")

	handler := reg.Handler()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, `version="unknown"`)
	assert.Contains(t, body, `commit="unknown"`)
}
