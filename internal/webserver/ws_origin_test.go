package webserver

import (
	"net/http"
	"testing"
)

func TestHostOnly(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"localhost:8080", "localhost"},
		{"127.0.0.1:9", "127.0.0.1"},
		{"example.com", "example.com"},
		{"[::1]:8443", "::1"},
	}
	for _, tc := range cases {
		if got := hostOnly(tc.in); got != tc.want {
			t.Errorf("hostOnly(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestWebsocketSameHostOrigin(t *testing.T) {
	t.Parallel()
	mk := func(host, origin string) *http.Request {
		r := &http.Request{Header: make(http.Header), Host: host}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}
	if !websocketSameHostOrigin(mk("app.internal:443", "https://app.internal")) {
		t.Fatal("expected same host (implicit port) to match")
	}
	if !websocketSameHostOrigin(mk("app.internal:8080", "http://app.internal:8080")) {
		t.Fatal("expected explicit matching ports to match")
	}
	if websocketSameHostOrigin(mk("good.example", "https://evil.example")) {
		t.Fatal("expected different hosts to reject")
	}
	if !websocketSameHostOrigin(mk("localhost:3000", "")) {
		t.Fatal("expected empty Origin to allow (non-browser clients)")
	}
}
