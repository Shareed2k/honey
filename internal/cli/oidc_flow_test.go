package cli

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"golang.org/x/oauth2"
)

func TestBuildAuthCodeURL(t *testing.T) {
	conf := &oauth2.Config{
		ClientID:    "honey-kube",
		Endpoint:    oauth2.Endpoint{AuthURL: "https://issuer.example/auth", TokenURL: "https://issuer.example/token"},
		RedirectURL: "http://127.0.0.1:12345/callback",
		Scopes:      []string{"openid", "email", "groups"},
	}

	got := buildAuthCodeURL(conf, "the-state", "the-verifier", "the-nonce")
	u, err := url.Parse(got)
	require.NoError(t, err)
	q := u.Query()

	require.Equal(t, "the-state", q.Get("state"))
	require.Equal(t, "the-nonce", q.Get("nonce"))
	require.Equal(t, "S256", q.Get("code_challenge_method"))
	require.Equal(t, "http://127.0.0.1:12345/callback", q.Get("redirect_uri"))
	require.Equal(t, "honey-kube", q.Get("client_id"))
	require.Equal(t, "code", q.Get("response_type"))
	require.Contains(t, q.Get("scope"), "openid")

	// code_challenge must be base64url(sha256(verifier)) — i.e. genuine S256.
	sum := sha256.Sum256([]byte("the-verifier"))
	require.Equal(t, base64.RawURLEncoding.EncodeToString(sum[:]), q.Get("code_challenge"))
}

// newStubIssuer starts a minimal OIDC issuer: discovery + a token endpoint that
// returns idToken regardless of the code. It performs no client-side id_token
// verification (the honey web server does that), so no JWKS/signing is needed.
func newStubIssuer(t *testing.T, idToken string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                srv.URL,
			"authorization_endpoint":                srv.URL + "/auth",
			"token_endpoint":                        srv.URL + "/token",
			"jwks_uri":                              srv.URL + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     idToken,
		})
	})
	return srv
}

// newStubAdmin serves the honey /api/v1/kube/oidc-config endpoint pointing at
// issuerURL with the given client id.
func newStubAdmin(t *testing.T, issuerURL, clientID string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/kube/oidc-config", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":    issuerURL,
			"client_id": clientID,
			"scopes":    []string{"groups"},
		})
	})
	return httptest.NewServer(mux)
}

func TestBrowserAuthCodeFlow(t *testing.T) {
	// IgnoreCurrent snapshots package-global daemons (ants pool, engine tunnel
	// sweeper) started elsewhere so VerifyNone catches only leaks from the flow.
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	issuer := newStubIssuer(t, "the-id-token")
	defer issuer.Close()
	admin := newStubAdmin(t, issuer.URL, "honey-kube")
	defer admin.Close()
	defer http.DefaultClient.CloseIdleConnections()

	urlCh := make(chan string, 1)
	orig := authURLPrinter
	authURLPrinter = func(u string) { urlCh <- u }
	defer func() { authURLPrinter = orig }()

	type flowResult struct {
		idToken string
		nonce   string
		err     error
	}
	done := make(chan flowResult, 1)
	go func() {
		id, nonce, err := browserAuthCodeFlow(context.Background(), admin.URL, []string{"offline_access"})
		done <- flowResult{id, nonce, err}
	}()

	var authURL string
	select {
	case authURL = <-urlCh:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for auth URL")
	}

	u, err := url.Parse(authURL)
	require.NoError(t, err)
	q := u.Query()
	require.Equal(t, "S256", q.Get("code_challenge_method"))
	require.NotEmpty(t, q.Get("code_challenge"))
	require.Contains(t, q.Get("scope"), "offline_access")
	require.Contains(t, q.Get("scope"), "groups")

	state := q.Get("state")
	nonce := q.Get("nonce")
	redirect := q.Get("redirect_uri")
	require.NotEmpty(t, state)
	require.NotEmpty(t, nonce)
	require.NotEmpty(t, redirect)

	resp, err := http.Get(redirect + "?code=auth-code&state=" + url.QueryEscape(state))
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	var res flowResult
	select {
	case res = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for flow result")
	}
	require.NoError(t, res.err)
	require.Equal(t, "the-id-token", res.idToken)
	require.Equal(t, nonce, res.nonce)
}

func TestBrowserAuthCodeFlow_StateMismatch(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	issuer := newStubIssuer(t, "tok")
	defer issuer.Close()
	admin := newStubAdmin(t, issuer.URL, "honey-kube")
	defer admin.Close()
	defer http.DefaultClient.CloseIdleConnections()

	urlCh := make(chan string, 1)
	orig := authURLPrinter
	authURLPrinter = func(u string) { urlCh <- u }
	defer func() { authURLPrinter = orig }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, _, err := browserAuthCodeFlow(ctx, admin.URL, nil)
		done <- err
	}()

	var authURL string
	select {
	case authURL = <-urlCh:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for auth URL")
	}
	u, err := url.Parse(authURL)
	require.NoError(t, err)
	redirect := u.Query().Get("redirect_uri")

	resp, err := http.Get(redirect + "?code=auth-code&state=WRONG")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for flow result")
	}
}

func TestWriteSSHCertFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "honey_ed25519-cert.pub")
	const cert = "ssh-ed25519-cert-v01@openssh.com AAAAtest alice@corp"

	require.NoError(t, writeSSHCertFile(path, cert))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, cert+"\n", string(data))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestLoadOrCreateSSHIdentity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys", "honey_ed25519")

	s1, err := loadOrCreateSSHIdentity(path)
	require.NoError(t, err)
	require.NotNil(t, s1)

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	// A second call loads the existing key rather than regenerating it.
	s2, err := loadOrCreateSSHIdentity(path)
	require.NoError(t, err)
	require.Equal(t, s1.PublicKey().Marshal(), s2.PublicKey().Marshal())
}

func TestAuthURLPrinter_OpensBrowserByDefault(t *testing.T) {
	var capturedURL string
	origFn := openBrowserFn
	openBrowserFn = func(url string) error {
		capturedURL = url
		return nil
	}
	t.Cleanup(func() { openBrowserFn = origFn })

	testURL := "https://idp.example/auth?x=1"
	authURLPrinter(testURL)

	require.Equal(t, testURL, capturedURL)
}

func TestAuthURLPrinter_NoBrowserSuppressesOpen(t *testing.T) {
	orig := oidcNoBrowser
	oidcNoBrowser = true
	t.Cleanup(func() { oidcNoBrowser = orig })

	openBrowserCalled := false
	origFn := openBrowserFn
	openBrowserFn = func(_ string) error {
		openBrowserCalled = true
		t.Fatal("openBrowser should not be called when oidcNoBrowser is true")
		return nil
	}
	t.Cleanup(func() { openBrowserFn = origFn })

	authURLPrinter("https://idp.example/auth?x=1")

	require.False(t, openBrowserCalled)
}
