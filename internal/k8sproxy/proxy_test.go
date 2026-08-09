package k8sproxy

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"k8s.io/client-go/rest"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// echoHeaders is a fake Kubernetes API server: it records the request it
// received and echoes back the headers, method, and path/query as JSON.
type echoResponse struct {
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Headers map[string][]string `json:"headers"`
}

func newEchoServer(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := echoResponse{
			Method:  r.Method,
			Path:    r.URL.Path,
			Headers: map[string][]string(r.Header),
		}
		if r.URL.RawQuery != "" {
			resp.Path += "?" + r.URL.RawQuery
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTeapot)
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func newInboundRequest(t *testing.T, target string) *http.Request {
	t.Helper()

	r, err := http.NewRequest(http.MethodGet, target, nil)
	require.NoError(t, err)
	return r
}

func TestNewClusterProxy_BadHost(t *testing.T) {
	_, err := newClusterProxy(&rest.Config{Host: "http://%zz"})
	require.Error(t, err)
}

func TestClusterProxy_Serve_ImpersonationAndStrip(t *testing.T) {
	srv := newEchoServer(t)
	defer srv.Close()

	p, err := newClusterProxy(&rest.Config{Host: srv.URL})
	require.NoError(t, err)

	r := newInboundRequest(t, "http://honey.local/api/v1/namespaces/default/pods")
	r.Header.Set("Impersonate-User", "cluster-admin")
	r.Header.Add("Impersonate-Group", "system:masters")
	r.Header.Set("Impersonate-Extra-Foo", "bar")
	r.Header.Set("Authorization", "Bearer evil")

	w := httptest.NewRecorder()
	p.serve(w, r, Identity{User: "alice", Groups: []string{"dev", "ops"}})

	require.Equal(t, http.StatusTeapot, w.Code)

	var resp echoResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	require.Equal(t, []string{"alice"}, resp.Headers["Impersonate-User"])
	require.Equal(t, []string{"dev", "ops"}, resp.Headers["Impersonate-Group"])

	_, hasExtra := resp.Headers["Impersonate-Extra-Foo"]
	require.False(t, hasExtra, "client-supplied Impersonate-Extra-Foo must be stripped")

	require.NotContains(t, resp.Headers["Impersonate-User"], "cluster-admin")
	require.NotContains(t, resp.Headers["Impersonate-Group"], "system:masters")

	auth, hasAuth := resp.Headers["Authorization"]
	if hasAuth {
		require.NotContains(t, auth, "Bearer evil")
	}
}

func TestClusterProxy_Serve_PathAndQueryForwarded(t *testing.T) {
	srv := newEchoServer(t)
	defer srv.Close()

	p, err := newClusterProxy(&rest.Config{Host: srv.URL})
	require.NoError(t, err)

	r := newInboundRequest(t, "http://honey.local/api/v1/namespaces/default/pods?limit=5")

	w := httptest.NewRecorder()
	p.serve(w, r, Identity{User: "alice"})

	require.Equal(t, http.StatusTeapot, w.Code)

	var resp echoResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "/api/v1/namespaces/default/pods?limit=5", resp.Path)
}

func TestClusterProxy_Serve_ResponsePassthrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	p, err := newClusterProxy(&rest.Config{Host: srv.URL})
	require.NoError(t, err)

	r := newInboundRequest(t, "http://honey.local/healthz")
	w := httptest.NewRecorder()
	p.serve(w, r, Identity{User: "alice"})

	require.Equal(t, http.StatusAccepted, w.Code)
	require.Equal(t, `{"ok":true}`, w.Body.String())
}

// TestClusterProxy_Serve_UpgradePassthrough drives serve() through real
// listening front-end and back-end servers so that both ends of the pipe
// are hijackable net.Conns, exercising the actual SPDY/websocket-style
// upgrade path (kubectl exec/logs -f/port-forward) rather than a fallback.
func TestClusterProxy_Serve_UpgradePassthrough(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "" {
			w.WriteHeader(http.StatusOK)
			return
		}

		hj, ok := w.(http.Hijacker)
		require.True(t, ok, "backend ResponseWriter must support hijacking")
		conn, rw, err := hj.Hijack()
		require.NoError(t, err)
		defer conn.Close()

		_, err = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: SPDY/3.1\r\n\r\n")
		require.NoError(t, err)
		require.NoError(t, rw.Flush())

		buf := make([]byte, 1024)
		for {
			n, rerr := rw.Read(buf)
			if n > 0 {
				if _, werr := rw.Write(buf[:n]); werr != nil {
					return
				}
				if ferr := rw.Flush(); ferr != nil {
					return
				}
			}
			if rerr != nil {
				return
			}
		}
	}))
	defer backend.Close()

	p, err := newClusterProxy(&rest.Config{Host: backend.URL})
	require.NoError(t, err)

	require.EqualValues(t, -1, p.rp.FlushInterval, "FlushInterval must be -1 for prompt streaming")

	frontend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.serve(w, r, Identity{User: "alice"})
	}))
	defer frontend.Close()

	frontendURL, err := url.Parse(frontend.URL)
	require.NoError(t, err)

	conn, err := net.Dial("tcp", frontendURL.Host)
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.SetDeadline(time.Now().Add(10*time.Second)))

	req := "GET /apis/upgrade HTTP/1.1\r\n" +
		"Host: " + frontendURL.Host + "\r\n" +
		"Connection: Upgrade\r\n" +
		"Upgrade: SPDY/3.1\r\n" +
		"\r\n"
	_, err = conn.Write([]byte(req))
	require.NoError(t, err)

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	require.Equal(t, "SPDY/3.1", resp.Header.Get("Upgrade"))

	payload := []byte("hello-upgrade")
	_, err = conn.Write(payload)
	require.NoError(t, err)

	echoed := make([]byte, len(payload))
	_, err = io.ReadFull(br, echoed)
	require.NoError(t, err)
	require.Equal(t, payload, echoed)
}
