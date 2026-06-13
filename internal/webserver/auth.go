// Package webserver provides the embedded HTTP server for honey.
package webserver

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/safepath"
)

const (
	webTokenEnv  = "HONEY_WEB_TOKEN"
	webTokenFile = "web_token"
)

// generateToken returns a fresh random hex token.
func generateToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// AuthToken returns a fixed token from the environment when set, or generates a random hex token.
func AuthToken() (string, error) {
	if v := strings.TrimSpace(os.Getenv(webTokenEnv)); v != "" {
		return v, nil
	}
	return generateToken()
}

// ResolveToken returns a stable web auth token. Precedence:
//  1. HONEY_WEB_TOKEN env var, if set;
//  2. a token previously persisted at stateDir/web_token;
//  3. a freshly generated token, which is then persisted to stateDir/web_token.
//
// Persisting the generated token keeps it stable across restarts (e.g. a docker
// container with a mounted state volume) so a bookmarked ?token= URL keeps working.
// A persist failure is non-fatal: the generated (ephemeral) token is still returned.
func ResolveToken(stateDir string) (string, error) {
	if v := strings.TrimSpace(os.Getenv(webTokenEnv)); v != "" {
		return v, nil
	}
	path := filepath.Join(strings.TrimSpace(stateDir), webTokenFile)
	if stateDir != "" {
		if b, err := safepath.ReadFile(path); err == nil {
			if t := strings.TrimSpace(string(b)); t != "" {
				return t, nil
			}
		}
	}
	tok, err := generateToken()
	if err != nil {
		return "", err
	}
	if stateDir == "" {
		return tok, nil
	}
	if err := safepath.WriteFile(path, []byte(tok+"\n"), 0o600); err != nil {
		zap.L().Warn("could not persist web token; it will change on restart",
			zap.String("path", path), zap.Error(err))
	}
	return tok, nil
}

// setTokenCookie stores the validated token in the honey_proxy_token cookie so
// subsequent requests from the same browser are authorized without the ?token=
// query string. Secure is set only when the request reached us over TLS (directly
// or via a TLS-terminating reverse proxy), so the cookie also works over plain HTTP.
func setTokenCookie(w http.ResponseWriter, r *http.Request, value string) {
	// #nosec G124 -- dynamic secure flag is not recognized by gosec
	http.SetCookie(w, &http.Cookie{
		Name:     "honey_proxy_token",
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", // mitigate G124
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(24 * time.Hour),
	})
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
