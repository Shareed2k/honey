// Package webserver provides the embedded HTTP server for honey.
package webserver

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
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
	got := bearerFromRequest(r)
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

// trustedUserHeader carries the caller identity asserted by a trusted reverse
// proxy (the Grafana X-WEBAUTH-USER pattern). It is honored only for requests
// originating from a configured trusted-proxy network — never from arbitrary
// clients, which could otherwise forge any identity.
const trustedUserHeader = "X-Honey-User"

// contextKey is an unexported type so context values set here cannot collide
// with keys from other packages.
type contextKey int

const ctxActorKey contextKey = iota

// userFromRequest resolves the caller identity AFTER authentication passes.
// Priority: trusted-proxy header > JWT subject claim > "api" (legacy shared
// token, which carries no identity). The result feeds OPA policy input.
func userFromRequest(r *http.Request, trustedNets []*net.IPNet, jwtPubKey ed25519.PublicKey) string {
	if isTrustedProxy(r, trustedNets) {
		if u := strings.TrimSpace(r.Header.Get(trustedUserHeader)); u != "" {
			return u
		}
	}
	if bearer := bearerFromRequest(r); bearer != "" {
		if sub, err := jwtSubject(bearer, jwtPubKey); err == nil && sub != "" {
			return sub
		}
	}
	return "api"
}

// isTrustedProxy reports whether the request's immediate peer is within one of
// the configured trusted-proxy networks. Empty config means no peer is trusted.
func isTrustedProxy(r *http.Request, nets []*net.IPNet) bool {
	if len(nets) == 0 {
		return false
	}
	ipStr, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ipStr = r.RemoteAddr
	}
	ip := net.ParseIP(strings.TrimSpace(ipStr))
	if ip == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// bearerFromRequest extracts the bearer token from the Authorization header, or
// "" when absent. Shared by token auth and JWT identity resolution.
func bearerFromRequest(r *http.Request) string {
	v := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(v), "bearer ") {
		return strings.TrimSpace(v[7:])
	}
	return ""
}

// jwtSubject parses and verifies an Ed25519-signed JWT, returning its subject
// claim. Returns an error when no key is configured, the signature is invalid,
// or the subject is empty.
func jwtSubject(tokenStr string, pubKey ed25519.PublicKey) (string, error) {
	if pubKey == nil {
		return "", fmt.Errorf("no public key configured")
	}
	tok, err := jwt.ParseWithClaims(tokenStr, &jwt.RegisteredClaims{},
		func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return pubKey, nil
		})
	if err != nil {
		return "", err
	}
	c, ok := tok.Claims.(*jwt.RegisteredClaims)
	if !ok || c.Subject == "" {
		return "", fmt.Errorf("missing sub claim")
	}
	return c.Subject, nil
}

// actorFromCtx returns the resolved caller identity stored by authMiddleware,
// defaulting to "api" when none was set (e.g. auth disabled, or a non-HTTP path).
func actorFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(ctxActorKey).(string); ok && v != "" {
		return v
	}
	return "api"
}
