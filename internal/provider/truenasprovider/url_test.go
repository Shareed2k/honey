package truenasprovider

import "testing"

func TestNormalizeWSURL(t *testing.T) {
	tests := []struct {
		in       string
		insecure bool
		wantWS   string
		wantHost string
	}{
		{"https://nas.example.com", false, "wss://nas.example.com/api/current", "nas.example.com"},
		{"http://10.0.0.5", true, "ws://10.0.0.5/api/current", "10.0.0.5"},
		{"wss://nas.example.com/api/current", false, "wss://nas.example.com/api/current", "nas.example.com"},
		{"10.0.0.8:443", false, "wss://10.0.0.8:443/api/current", "10.0.0.8"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			gotWS, gotHost, err := normalizeWSURL(tc.in, tc.insecure)
			if err != nil {
				t.Fatal(err)
			}
			if gotWS != tc.wantWS {
				t.Errorf("wsURL: got %q want %q", gotWS, tc.wantWS)
			}
			if gotHost != tc.wantHost {
				t.Errorf("host: got %q want %q", gotHost, tc.wantHost)
			}
		})
	}
}
