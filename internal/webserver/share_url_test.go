package webserver

import (
	"errors"
	"testing"
)

func TestShareURL(t *testing.T) {
	fakeLAN := func() (string, error) { return "10.1.2.3", nil }
	failLAN := func() (string, error) { return "", errors.New("no route") }

	tests := []struct {
		name       string
		publicURL  string
		listenAddr string
		resolveLAN func() (string, error)
		want       string
		wantErr    bool
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
			name:       "localhost bind resolves lan ip",
			listenAddr: "localhost:8765",
			resolveLAN: fakeLAN,
			want:       "http://10.1.2.3:8765",
		},
		{
			name:       "loopback ip bind resolves lan ip",
			listenAddr: "127.0.0.1:8765",
			resolveLAN: fakeLAN,
			want:       "http://10.1.2.3:8765",
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
			name:       "resolveLAN error on loopback listen returns error",
			listenAddr: "127.0.0.1:8765",
			resolveLAN: failLAN,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := shareBaseURL(tt.publicURL, tt.listenAddr, tt.resolveLAN)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("shareBaseURL(%q, %q) = %q, want error", tt.publicURL, tt.listenAddr, got)
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
