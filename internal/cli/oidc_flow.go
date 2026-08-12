package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// kubeOIDCConfigPath is the honey web endpoint returning the non-secret OIDC
// discovery values (issuer, client id, scopes) the login flow starts from.
const kubeOIDCConfigPath = "/api/v1/kube/oidc-config"

// callbackPageHTML is the tiny page the loopback server returns after receiving
// the authorization code, telling the user they may return to the terminal.
const callbackPageHTML = `<!doctype html><html><head><meta charset="utf-8">` +
	`<title>Sign-in complete</title></head>` +
	`<body style="font-family:sans-serif;text-align:center;padding-top:3rem">` +
	`<h2>Sign-in complete</h2>` +
	`<p>You may close this tab and return to the terminal.</p></body></html>`

// openBrowserFn is a seam so tests can override the OS-specific browser open
// without shelling out.
var openBrowserFn = openBrowser

// authURLPrinter surfaces the sign-in URL to the user. It is a package variable
// so tests can capture the URL instead of printing it; production attempts to
// open the browser (best-effort) unless oidcNoBrowser is set, and always prints
// the URL to stderr as a fallback.
var authURLPrinter = func(authURL string) {
	if !oidcNoBrowser {
		_ = openBrowserFn(authURL) // best-effort; the printed URL below is the fallback
	}
	fmt.Fprintf(os.Stderr, "\nOpen this URL in your browser to sign in:\n\n  %s\n\n", authURL)
}

// oidcPublicConfig mirrors the honey /kube/oidc-config response.
type oidcPublicConfig struct {
	Issuer   string   `json:"issuer"`
	ClientID string   `json:"client_id"`
	Scopes   []string `json:"scopes"`
}

// browserAuthCodeFlow runs an OIDC authorization-code flow with PKCE over a
// loopback redirect and returns the resulting id_token plus the nonce it was
// issued for (the caller forwards both to the honey login endpoint, which binds
// the nonce during verification). It fetches the public OIDC config from
// adminURL, discovers the issuer's endpoints, prints the authorize URL for the
// user to open, waits for the loopback callback, and exchanges the code. The
// loopback server is shut down before returning (goleak-clean). extraScopes are
// merged on top of openid/email/groups and the config's scopes.
func browserAuthCodeFlow(ctx context.Context, adminURL string, extraScopes []string) (idToken, nonce string, err error) {
	cfg, err := fetchOIDCConfig(ctx, adminURL)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(cfg.Issuer) == "" || strings.TrimSpace(cfg.ClientID) == "" {
		return "", "", fmt.Errorf("oidc login is not configured on the honey server")
	}

	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return "", "", fmt.Errorf("discover oidc issuer %q: %w", cfg.Issuer, err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", fmt.Errorf("start loopback listener: %w", err)
	}
	defer func() {
		// Closed by waitForCallback's Shutdown on the happy path; this covers the
		// early-return paths below so the listener never leaks.
		_ = ln.Close()
	}()

	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return "", "", fmt.Errorf("loopback listener has no TCP address")
	}
	redirectURL := fmt.Sprintf("http://127.0.0.1:%d/callback", addr.Port)

	verifier := oauth2.GenerateVerifier()
	state, err := randomURLToken()
	if err != nil {
		return "", "", err
	}
	nonce, err = randomURLToken()
	if err != nil {
		return "", "", err
	}

	conf := &oauth2.Config{
		ClientID:    cfg.ClientID,
		Endpoint:    provider.Endpoint(),
		RedirectURL: redirectURL,
		Scopes:      dedupScopes([]string{"openid", "email", "groups"}, cfg.Scopes, extraScopes),
	}

	authURLPrinter(buildAuthCodeURL(conf, state, verifier, nonce))

	code, err := waitForCallback(ctx, ln, state)
	if err != nil {
		return "", "", err
	}

	tok, err := conf.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return "", "", fmt.Errorf("exchange authorization code: %w", err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok || strings.TrimSpace(rawID) == "" {
		return "", "", fmt.Errorf("token response did not include an id_token")
	}
	return rawID, nonce, nil
}

// buildAuthCodeURL builds the authorization-request URL, attaching the S256 PKCE
// challenge derived from verifier and the OIDC nonce. Split out so tests can
// assert the URL parameters without a browser.
func buildAuthCodeURL(conf *oauth2.Config, state, verifier, nonce string) string {
	return conf.AuthCodeURL(state,
		oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("nonce", nonce),
	)
}

// oidcCallbackResult carries the loopback handler's outcome to the waiter.
type oidcCallbackResult struct {
	code string
	err  error
}

// waitForCallback serves the loopback listener until the OIDC redirect arrives,
// validates that state matches, and returns the authorization code. The server
// is always shut down before returning, so it leaks no goroutine. A canceled
// ctx aborts the wait.
func waitForCallback(ctx context.Context, ln net.Listener, expectedState string) (string, error) {
	resultCh := make(chan oidcCallbackResult, 1)
	send := func(r oidcCallbackResult) {
		select {
		case resultCh <- r:
		default:
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != expectedState {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			send(oidcCallbackResult{err: fmt.Errorf("oidc callback state mismatch")})
			return
		}
		if e := strings.TrimSpace(q.Get("error")); e != "" {
			http.Error(w, "authorization failed", http.StatusBadRequest)
			send(oidcCallbackResult{err: fmt.Errorf("authorization error: %s", e)})
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, callbackPageHTML)
		send(oidcCallbackResult{code: q.Get("code")})
	})

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = srv.Serve(ln)
	}()

	var (
		code string
		err  error
	)
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case res := <-resultCh:
		code, err = res.code, res.err
		if err == nil && code == "" {
			err = fmt.Errorf("oidc callback did not carry an authorization code")
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	<-serveDone
	return code, err
}

// fetchOIDCConfig GETs the honey server's public OIDC config.
func fetchOIDCConfig(ctx context.Context, adminURL string) (oidcPublicConfig, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, adminURL+kubeOIDCConfigPath, nil)
	if err != nil {
		return oidcPublicConfig{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return oidcPublicConfig{}, fmt.Errorf("fetch oidc config: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		return oidcPublicConfig{}, fmt.Errorf("oidc login is not enabled on the honey server")
	}
	if resp.StatusCode != http.StatusOK {
		return oidcPublicConfig{}, fmt.Errorf("fetch oidc config: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var cfg oidcPublicConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return oidcPublicConfig{}, fmt.Errorf("parse oidc config: %w", err)
	}
	return cfg, nil
}

// randomURLToken returns 32 bytes of crypto/rand entropy as a base64url string,
// used for the state and nonce values.
func randomURLToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// dedupScopes flattens the scope groups into a single slice, trimming blanks and
// dropping duplicates while preserving first-seen order.
func dedupScopes(groups ...[]string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, g := range groups {
		for _, s := range g {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			if _, dup := seen[s]; dup {
				continue
			}
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}
