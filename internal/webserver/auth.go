// Package webserver provides the embedded HTTP server for honey.
package webserver

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"strings"
)

const webTokenEnv = "HONEY_WEB_TOKEN"

// AuthToken returns a fixed token from the environment when set, or generates a random hex token.
func AuthToken() (string, error) {
	if v := strings.TrimSpace(os.Getenv(webTokenEnv)); v != "" {
		return v, nil
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func tokenFromRequest(r *http.Request, token string) bool {
	got := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(got), "bearer ") {
		got = strings.TrimSpace(got[7:])
	}
	if got == "" {
		got = strings.TrimSpace(r.Header.Get("X-Honey-Token"))
	}
	if got == "" {
		got = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	if got == "" {
		if cookie, err := r.Cookie("honey_proxy_token"); err == nil {
			got = strings.TrimSpace(cookie.Value)
		}
	}
	return got != "" && got == token
}
