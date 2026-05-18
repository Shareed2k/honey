package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddleware_preservesHijacker(t *testing.T) {
	reg := NewRegistry("test", "test")
	var inner http.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, ok := w.(http.Hijacker); !ok {
			t.Fatal("inner handler: ResponseWriter is not http.Hijacker")
		}
		w.WriteHeader(http.StatusOK)
	})
	h := reg.Middleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/ws/ssh", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
