package k8sproxy

import (
	"context"
	"net/http"
	"strings"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/policy"
)

// handler is the live k8s access-proxy HTTP handler. Every request must present
// a verified client certificate (the serving TLS config enforces
// RequireAndVerifyClientCert); the certificate's CommonName is the actor. The
// first path segment names the target cluster. Requests are OPA-gated
// (fail-closed) and audited before being forwarded to the cluster's API server
// under honey's impersonated identity.
type handler struct {
	reg  *Registry
	enf  *policy.Enforcer
	sink audit.Sink
}

// NewHandler builds the proxy handler. A nil enforcer disables the policy gate
// (allow). A nil sink is replaced with a no-op sink.
func NewHandler(reg *Registry, enf *policy.Enforcer, sink audit.Sink) http.Handler {
	if sink == nil {
		sink = audit.NewNoopSink()
	}
	return &handler{reg: reg, enf: enf, sink: sink}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorFromCert(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// The boundary returns a generic 404 for an unknown cluster (and for an
	// actor with no identity mapping) so it never reveals which clusters exist.
	cluster, upstreamPath := splitClusterPath(r.URL.Path)
	if cluster == "" || !h.reg.Has(cluster) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// Groups come from the client certificate's O= fields (honey-CA attested);
	// they feed both the impersonated identity and the OPA gate. Empty ⇒ the
	// cluster's DefaultGroups fallback.
	groups := certGroups(r)
	ident, ok := h.reg.IdentityForWithGroups(cluster, actor, groups)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	r.URL.Path = upstreamPath
	r.URL.RawPath = ""
	ri := parseRequestInfo(r.Method, upstreamPath, r.URL.RawQuery)

	// Gate against the EFFECTIVE impersonated groups (ident.Groups, after the
	// DefaultGroups fallback), so authorization matches the identity actually
	// forwarded to the API server.
	if !h.gate(w, r, actor, cluster, ident.Groups, ri) {
		return
	}

	h.logAudit(r.Context(), actor, cluster, "", ident.Groups, ri, "allow")
	h.reg.Serve(w, r, cluster, ident)
}

// gate evaluates the OPA policy. It returns true when the request may proceed.
// On denial (or, fail-closed, on an evaluation error) it audits the deny, writes
// a 403, and returns false. A nil enforcer allows.
func (h *handler) gate(w http.ResponseWriter, r *http.Request, actor, cluster string, groups []string, ri RequestInfo) bool {
	if h.enf == nil {
		return true
	}
	d, err := h.enf.Evaluate(r.Context(), map[string]any{
		"action":         "k8s_request",
		"actor":          actor,
		"cluster":        cluster,
		"groups":         groups,
		"cluster_labels": h.reg.LabelsFor(cluster),
		"verb":           ri.Verb,
		"resource":       ri.Resource,
		"namespace":      ri.Namespace,
		"name":           ri.Name,
		"subresource":    ri.Subresource,
	})
	if err != nil {
		// Fail closed: a broken policy denies rather than opening the boundary.
		h.logAudit(r.Context(), actor, cluster, "policy evaluation error", groups, ri, "deny")
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	if !d.Allow {
		reason := strings.TrimSpace(d.DenyReason)
		if reason == "" {
			reason = "forbidden by policy"
		}
		h.logAudit(r.Context(), actor, cluster, reason, groups, ri, "deny")
		http.Error(w, reason, http.StatusForbidden)
		return false
	}
	return true
}

// logAudit records one k8s_request decision. It never logs certificate or
// service-account material — only the actor CN, cluster, and parsed request
// shape.
func (h *handler) logAudit(ctx context.Context, actor, cluster, reason string, groups []string, ri RequestInfo, decision string) {
	if h.sink == nil {
		return
	}
	_ = h.sink.Log(ctx, audit.Event{
		Source:     "web",
		Actor:      actor,
		Action:     "k8s_request",
		Target:     cluster,
		Decision:   decision,
		DenyReason: reason,
		Extra: map[string]string{
			"groups":      strings.Join(groups, ","),
			"verb":        ri.Verb,
			"resource":    ri.Resource,
			"namespace":   ri.Namespace,
			"name":        ri.Name,
			"subresource": ri.Subresource,
		},
	})
}

// actorFromCert extracts the actor from the verified client certificate's
// CommonName. ok is false when there is no verified peer certificate or its CN
// is empty (both map to 401 at the boundary).
func actorFromCert(r *http.Request) (string, bool) {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return "", false
	}
	cn := strings.TrimSpace(r.TLS.PeerCertificates[0].Subject.CommonName)
	if cn == "" {
		return "", false
	}
	return cn, true
}

// certGroups returns the verified client certificate's Organization (O=) fields
// as the actor's groups. These are honey-CA attested (only honey's device CA
// signs them), never client-asserted. Order is not significant (a pkix SET);
// callers treat groups as a set.
func certGroups(r *http.Request) []string {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return nil
	}
	return r.TLS.PeerCertificates[0].Subject.Organization
}

// splitClusterPath splits an inbound path "/<cluster>/<rest>" into the cluster
// name and the upstream path (the path with the /<cluster> prefix removed).
// The upstream path defaults to "/" when nothing follows the cluster segment.
func splitClusterPath(p string) (cluster, upstream string) {
	trimmed := strings.TrimPrefix(p, "/")
	idx := strings.IndexByte(trimmed, '/')
	if idx < 0 {
		return trimmed, "/"
	}
	upstream = trimmed[idx:]
	if upstream == "" {
		upstream = "/"
	}
	return trimmed[:idx], upstream
}
