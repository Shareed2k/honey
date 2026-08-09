package webserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/jit"
	"github.com/shareed2k/honey/internal/sshca"
)

// handleJITRedeemStatus (code-authenticated, no session token) reports what a
// share link offers WITHOUT consuming a redemption, so a link recipient can
// see a status/lobby view before deciding how to redeem. It never returns the
// code or its hash. Every lookup failure collapses to a generic 404 — unknown,
// expired, revoked, and denied codes are indistinguishable.
func (s *Server) handleJITRedeemStatus(w http.ResponseWriter, r *http.Request) {
	if s.opts.Jit == nil {
		httpError(w, fmt.Errorf("jit not enabled"), http.StatusServiceUnavailable)
		return
	}

	code := chi.URLParam(r, "code")
	g, err := s.opts.Jit.Peek(code)
	if err != nil {
		httpError(w, fmt.Errorf("invalid or expired link"), http.StatusNotFound)
		return
	}
	active := s.opts.Jit.Active(g.ID)

	resp := map[string]any{
		"status": string(g.Status),
		"active": active,
		"resource": map[string]any{
			"name":     g.Resource.Name,
			"provider": g.Resource.Provider,
		},
		"capabilities": g.Capabilities,
		"delivery":     string(g.Delivery),
		"offers": map[string]any{
			"web":  jitOffersWeb(g),
			"cert": jitOffersCert(g),
		},
	}
	if !g.ExpiresAt.IsZero() {
		resp["expires_at"] = g.ExpiresAt.Format(time.RFC3339)
	}
	writeJSON(w, resp)
}

// handleJITRedeemCert (code-authenticated, no session token) consumes a
// redemption on a cert-delivery grant and mints a short-lived SSH user
// certificate scoped to it. Delivery, capability, and active-window checks all
// run against a Peek BEFORE the redemption is consumed; every rejection —
// including the race where the grant expires or hits its redemption cap
// between the Peek and the Redeem — collapses to the same generic 404.
func (s *Server) handleJITRedeemCert(w http.ResponseWriter, r *http.Request) {
	if s.opts.Jit == nil {
		httpError(w, fmt.Errorf("jit not enabled"), http.StatusServiceUnavailable)
		return
	}
	if s.sshCA == nil {
		httpError(w, fmt.Errorf("ssh ca not available"), http.StatusServiceUnavailable)
		return
	}

	code := chi.URLParam(r, "code")

	var body struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxEnrollBody)).Decode(&body); err != nil {
		httpError(w, fmt.Errorf("decode request: %w", err), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.PublicKey) == "" {
		httpError(w, fmt.Errorf("public_key is required"), http.StatusBadRequest)
		return
	}

	g, err := s.opts.Jit.Peek(code)
	if err != nil {
		httpError(w, fmt.Errorf("invalid or expired link"), http.StatusNotFound)
		return
	}
	if !jitOffersCert(g) || !s.opts.Jit.Active(g.ID) {
		httpError(w, fmt.Errorf("invalid or expired link"), http.StatusNotFound)
		return
	}

	// Validate everything derivable from the request and the peeked grant
	// BEFORE consuming a redemption, so a bad public key or a grant with no
	// resolvable principal (both client-side errors, returned as 4xx) never
	// burns a redemption — which for a one-time link would kill it outright.
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(body.PublicKey))
	if err != nil {
		httpError(w, fmt.Errorf("parse public key: %w", err), http.StatusBadRequest)
		return
	}
	principal := firstNonEmpty(g.Recipient, g.Resource.Meta["ssh_user"], g.Actor)
	if principal == "" {
		httpError(w, fmt.Errorf("no principal for grant"), http.StatusBadRequest)
		return
	}

	// Consume the redemption only now that every precondition has been
	// checked. This still races against a concurrent redeem hitting the cap
	// or the grant expiring in between; that race collapses to the same
	// generic 404 as everything else.
	redeemed, err := s.opts.Jit.Redeem(code)
	if err != nil {
		httpError(w, fmt.Errorf("invalid or expired link"), http.StatusNotFound)
		return
	}

	ttl := time.Until(redeemed.ExpiresAt)
	if ttl <= 0 {
		httpError(w, fmt.Errorf("invalid or expired link"), http.StatusNotFound)
		return
	}
	if ttl > jitMaxDuration {
		ttl = jitMaxDuration
	}

	cert, err := s.sshCA.Sign(sshca.SignRequest{
		PublicKey:  pub,
		KeyID:      redeemed.ID,
		Principals: []string{principal},
		TTL:        ttl,
	})
	if err != nil {
		httpError(w, fmt.Errorf("sign certificate: %w", err), http.StatusInternalServerError)
		return
	}

	_ = s.opts.AuditSink.Log(r.Context(), audit.Event{
		Source:     "web",
		Actor:      firstNonEmpty(redeemed.Recipient, "share:"+redeemed.ID),
		Action:     "jit_redeemed",
		Target:     redeemed.Resource.Name,
		Decision:   "allow",
		ApprovalID: redeemed.ID,
		Extra:      map[string]string{"delivery": "cert"},
	})

	writeJSON(w, map[string]any{
		"cert":              string(ssh.MarshalAuthorizedKey(cert)),
		"ca":                string(s.sshCA.AuthorizedKey()),
		"principals":        []string{principal},
		"valid_before_unix": cert.ValidBefore,
	})
}

// jitOffersWeb reports whether g's delivery + capabilities would let a
// redeemer use the (not-yet-built) browser-terminal redeem path.
func jitOffersWeb(g jit.Grant) bool {
	return (g.Delivery == jit.DeliveryWeb || g.Delivery == jit.DeliveryBoth) && hasCapability(g.Capabilities, jit.CapShell)
}

// jitOffersCert reports whether g's delivery + capabilities allow minting an
// SSH certificate via handleJITRedeemCert.
func jitOffersCert(g jit.Grant) bool {
	if g.Delivery != jit.DeliveryCert && g.Delivery != jit.DeliveryBoth {
		return false
	}
	return hasCapability(g.Capabilities, jit.CapShell) || hasCapability(g.Capabilities, jit.CapExec)
}

// hasCapability reports whether caps contains want.
func hasCapability(caps []jit.Capability, want jit.Capability) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}

// firstNonEmpty returns the first argument that is non-blank after trimming
// whitespace, or "" if all are blank.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}
