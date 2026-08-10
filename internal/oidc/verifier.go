// Package oidc verifies OpenID Connect id_tokens for honey's SSO login flow.
// It is verification-only: no signing, no HTTP handlers, no browser flow.
package oidc

import (
	"context"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
)

// Config describes the OIDC provider and how to read identity from its tokens.
type Config struct {
	Issuer        string
	ClientID      string
	UsernameClaim string // claim → Claims.Email/username (e.g. "email")
	GroupsClaim   string // claim → Claims.Groups (e.g. "groups")
}

// Claims are the verified fields honey reads from an id_token. Raw carries every
// claim for policy evaluation.
type Claims struct {
	Subject string
	Email   string
	Groups  []string
	Raw     map[string]any
}

// Verifier verifies id_tokens against a discovered OIDC provider and extracts
// the configured username/groups claims. It is safe for concurrent use.
type Verifier struct {
	verifier      *oidc.IDTokenVerifier
	usernameClaim string
	groupsClaim   string
}

// New performs OIDC discovery against cfg.Issuer and returns a Verifier bound to
// cfg.ClientID as the expected audience.
func New(ctx context.Context, cfg Config) (*Verifier, error) {
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc: discover issuer %q: %w", cfg.Issuer, err)
	}
	uc, gc := cfg.UsernameClaim, cfg.GroupsClaim
	if uc == "" {
		uc = "email"
	}
	if gc == "" {
		gc = "groups"
	}
	return &Verifier{
		verifier:      provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		usernameClaim: uc,
		groupsClaim:   gc,
	}, nil
}

// Verify checks signature, iss, aud, exp (go-oidc rejects alg:none) and the
// nonce, then extracts the configured username/groups claims. Fail-closed.
func (v *Verifier) Verify(ctx context.Context, rawIDToken, expectedNonce string) (Claims, error) {
	tok, err := v.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return Claims{}, fmt.Errorf("oidc: verify id_token: %w", err)
	}
	if tok.Nonce != expectedNonce {
		return Claims{}, fmt.Errorf("oidc: nonce mismatch")
	}
	var raw map[string]any
	if err := tok.Claims(&raw); err != nil {
		return Claims{}, fmt.Errorf("oidc: decode claims: %w", err)
	}
	c := Claims{Subject: tok.Subject, Raw: raw}
	if s, ok := raw[v.usernameClaim].(string); ok {
		c.Email = s
	}
	c.Groups = toStringSlice(raw[v.groupsClaim])
	return c, nil
}

func toStringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
