package reverseproxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestForwarder_New(t *testing.T) {
	target := &url.URL{Scheme: "http", Host: "localhost:3000"}
	f := New(target)
	if f == nil {
		t.Fatal("Forwarder is nil")
	}
	if f.ReverseProxy == nil {
		t.Fatal("ReverseProxy is nil")
	}
}

func TestForwarder_ServeHTTP(t *testing.T) {
	// Create a test upstream server
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("Failed to parse URL: %v", err)
	}
	f := New(u)

	// Create a test request
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	f.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status OK, got %d", w.Code)
	}
}
