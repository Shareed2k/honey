package webserver

import (
	"errors"
	"testing"
)

func TestShareURL(t *testing.T) {
	errNoRoute := errors.New("no route")
	fakeLAN := func() (string, error) { return "10.1.2.3", nil }
	failLAN := func() (string, error) { return "", errNoRoute }

	tests := []struct {
		name       string
		publicURL  string
		listenAddr string
		resolveLAN func() (string, error)
		want       string
		// wantErr is the error the case must return, asserted with errors.Is.
		wantErr error
	}{
		{
			name:      "public url wins with scheme and trailing slash",
			publicURL: "https://honey.example.com/",
			// listenAddr must be ignored entirely when publicURL is set.
			listenAddr: "0.0.0.0:8765",
			resolveLAN: failLAN,
			want:       "https://honey.example.com",
		},
		{
			name:       "public url wins without scheme",
			publicURL:  "honey.example.com:9000",
			listenAddr: "0.0.0.0:8765",
			resolveLAN: failLAN,
			want:       "http://honey.example.com:9000",
		},
		{
			name:       "concrete bind ip kept as-is",
			listenAddr: "10.0.0.5:8765",
			resolveLAN: failLAN,
			want:       "http://10.0.0.5:8765",
		},
		{
			name:       "wildcard bind resolves lan ip",
			listenAddr: "0.0.0.0:8765",
			resolveLAN: fakeLAN,
			want:       "http://10.1.2.3:8765",
		},
		{
			// The default (--listen localhost:8765) answers on loopback ONLY, so
			// substituting the LAN IP would hand out a URL nothing is listening
			// on. It must report ErrListenerLoopbackOnly instead of guessing.
			name:       "localhost bind reports loopback-only",
			listenAddr: "localhost:8765",
			resolveLAN: fakeLAN,
			wantErr:    ErrListenerLoopbackOnly,
		},
		{
			name:       "loopback ip bind reports loopback-only",
			listenAddr: "127.0.0.1:8765",
			resolveLAN: fakeLAN,
			wantErr:    ErrListenerLoopbackOnly,
		},
		{
			// 127.0.0.2 is not a canonical literal but IS a real loopback
			// address, and would leak verbatim into a share link without the
			// net.ParseIP check.
			name:       "non-canonical loopback ip reports loopback-only",
			listenAddr: "127.0.0.2:8765",
			resolveLAN: fakeLAN,
			wantErr:    ErrListenerLoopbackOnly,
		},
		{
			name:       "ipv6 loopback bind reports loopback-only",
			listenAddr: "[::1]:8765",
			resolveLAN: fakeLAN,
			wantErr:    ErrListenerLoopbackOnly,
		},
		{
			// A loopback bind behind a TLS reverse proxy is exactly what
			// --public-url is for: it must win over the loopback rejection.
			name:       "public url wins over loopback bind",
			publicURL:  "https://honey.example.com",
			listenAddr: "localhost:8765",
			resolveLAN: failLAN,
			want:       "https://honey.example.com",
		},
		{
			name:       "bare port bind resolves lan ip",
			listenAddr: ":8765",
			resolveLAN: fakeLAN,
			want:       "http://10.1.2.3:8765",
		},
		{
			name:       "ipv6 bind bracketed correctly",
			listenAddr: "[2001:db8::1]:8765",
			resolveLAN: failLAN,
			want:       "http://[2001:db8::1]:8765",
		},
		{
			name:       "ipv6 wildcard bind resolves lan ip",
			listenAddr: "[::]:8765",
			resolveLAN: fakeLAN,
			want:       "http://10.1.2.3:8765",
		},
		{
			// A wildcard bind IS reachable, so this is the only case that can
			// still fail on the resolver itself.
			name:       "resolveLAN error on wildcard listen returns error",
			listenAddr: "0.0.0.0:8765",
			resolveLAN: failLAN,
			wantErr:    errNoRoute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := shareBaseURL(tt.publicURL, tt.listenAddr, tt.resolveLAN)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("shareBaseURL(%q, %q) = %q, want error %v", tt.publicURL, tt.listenAddr, got, tt.wantErr)
				}
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("shareBaseURL(%q, %q) error = %v, want %v", tt.publicURL, tt.listenAddr, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("shareBaseURL(%q, %q) unexpected error: %v", tt.publicURL, tt.listenAddr, err)
			}
			if got != tt.want {
				t.Errorf("shareBaseURL(%q, %q) = %q, want %q", tt.publicURL, tt.listenAddr, got, tt.want)
			}
		})
	}
}
