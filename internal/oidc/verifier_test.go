package oidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/require"
)

// newTestIssuer starts an httptest OIDC issuer signing with a fresh RSA key and
// returns its URL plus a mint(claims) helper producing signed compact JWTs.
func newTestIssuer(t *testing.T) (issuer string, mint func(map[string]any) string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	const kid = "test-key"
	jwks := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: key.Public(), KeyID: kid, Algorithm: "RS256", Use: "sig"}}}

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": srv.URL, "jwks_uri": srv.URL + "/keys",
			"authorization_endpoint": srv.URL + "/auth", "token_endpoint": srv.URL + "/token",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(jwks) })

	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", kid))
	require.NoError(t, err)
	mint = func(claims map[string]any) string {
		tok, err := josejwt.Signed(signer).Claims(claims).Serialize()
		require.NoError(t, err)
		return tok
	}
	return srv.URL, mint
}

func baseClaims(issuer string, exp time.Time) map[string]any {
	return map[string]any{
		"iss": issuer, "aud": "honey-kube", "sub": "u-1",
		"email": "alice@corp", "groups": []string{"eng"},
		"nonce": "N", "exp": exp.Unix(), "iat": time.Now().Unix(),
	}
}

func TestVerify(t *testing.T) {
	issuer, mint := newTestIssuer(t)
	v, err := New(context.Background(), Config{Issuer: issuer, ClientID: "honey-kube", UsernameClaim: "email", GroupsClaim: "groups"})
	require.NoError(t, err)

	t.Run("valid", func(t *testing.T) {
		c, err := v.Verify(context.Background(), mint(baseClaims(issuer, time.Now().Add(time.Hour))), "N")
		require.NoError(t, err)
		require.Equal(t, "alice@corp", c.Email)
		require.Equal(t, []string{"eng"}, c.Groups)
		require.Equal(t, "u-1", c.Subject)
	})
	t.Run("wrong audience", func(t *testing.T) {
		cl := baseClaims(issuer, time.Now().Add(time.Hour))
		cl["aud"] = "someone-else"
		_, err := v.Verify(context.Background(), mint(cl), "N")
		require.Error(t, err)
	})
	t.Run("expired", func(t *testing.T) {
		_, err := v.Verify(context.Background(), mint(baseClaims(issuer, time.Now().Add(-time.Hour))), "N")
		require.Error(t, err)
	})
	t.Run("nonce mismatch", func(t *testing.T) {
		_, err := v.Verify(context.Background(), mint(baseClaims(issuer, time.Now().Add(time.Hour))), "DIFFERENT")
		require.Error(t, err)
	})
	t.Run("bad signature", func(t *testing.T) {
		// sign with a key that is not published in the issuer's JWKS
		otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: otherKey},
			(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test-key"))
		require.NoError(t, err)
		tok, err := josejwt.Signed(signer).Claims(baseClaims(issuer, time.Now().Add(time.Hour))).Serialize()
		require.NoError(t, err)
		_, err = v.Verify(context.Background(), tok, "N")
		require.Error(t, err)
	})
	t.Run("alg none", func(t *testing.T) {
		_, err := v.Verify(context.Background(), unsignedToken(t, baseClaims(issuer, time.Now().Add(time.Hour))), "N")
		require.Error(t, err)
	})
}

// unsignedToken hand-builds an alg:none compact JWT (header.payload. with an
// empty signature) to assert the verifier rejects unsigned tokens.
func unsignedToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		require.NoError(t, err)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	header := map[string]any{"alg": "none", "typ": "JWT"}
	return enc(header) + "." + enc(claims) + "."
}
