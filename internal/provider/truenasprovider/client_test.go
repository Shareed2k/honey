package truenasprovider

import (
	"strings"
	"testing"
)

func TestAuthResponseError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		responseType string
		wsURL        string
		wantErr      bool
		contains     string
	}{
		{"SUCCESS", "", false, ""},
		{"success", "", false, ""},
		{"AUTH_ERR", "wss://nas/api/current", true, "invalid API key or username"},
		{"AUTH_ERR", "ws://nas/api/current", true, "https://"},
		{"auth_err", "", true, "invalid API key or username"},
		{"EXPIRED", "", true, "expired"},
		{"OTP_REQUIRED", "", true, "two-factor"},
		{"REDIRECT", "", true, "redirect-based"},
		{"UNKNOWN", "", true, "unexpected response_type"},
		{"", "", true, "empty response_type"},
	}
	for _, tc := range tests {
		t.Run(tc.responseType+"_"+tc.wsURL, func(t *testing.T) {
			t.Parallel()
			err := authResponseError(tc.responseType, tc.wsURL)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if tc.contains != "" && !strings.Contains(err.Error(), tc.contains) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.contains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
