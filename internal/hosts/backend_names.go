package hosts

import "strings"

// ParseBackendNames splits a comma-separated list of backend names (trimmed, lowercased).
// Empty input returns nil.
func ParseBackendNames(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(strings.ToLower(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

type backendFilter struct {
	name string
	kind string // empty = match any provider kind (legacy name-only token)
}

func parseBackendFilter(token string) backendFilter {
	token = strings.TrimSpace(strings.ToLower(token))
	if i := strings.Index(token, ":"); i >= 0 {
		return backendFilter{
			kind: strings.TrimSpace(token[:i]),
			name: strings.TrimSpace(token[i+1:]),
		}
	}
	return backendFilter{name: token}
}

func providerKindMatches(p Backend, kind string) bool {
	id := strings.ToLower(strings.TrimSpace(p.ID()))
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return true
	}
	if id == kind {
		return true
	}
	// YAML backends.kubernetes vs search provider id k8s
	if (kind == "kubernetes" || kind == "k8s") && id == "k8s" {
		return true
	}
	return false
}

// backendMatchesFilter reports whether backend p matches filter token f.
// localNames is the set of names owned by any locally-configured backend
// (honey or native), used to decide when a honey proxy should stay
// transparent: a token that resolves to a local backend is handled by that
// backend, so only tokens naming nothing local are presumed to live upstream
// and forwarded by the honey proxies.
func backendMatchesFilter(p Backend, f backendFilter, localNames map[string]struct{}) bool {
	if p.ID() == "honey" {
		n := strings.TrimSpace(strings.ToLower(p.BackendName()))
		if f.name != "" && n == f.name {
			return true // token names THIS honey proxy explicitly
		}
		if f.name != "" {
			if _, isLocal := localNames[f.name]; isLocal {
				return false // token names another local backend, not this proxy
			}
		}
		return true // token names nothing local (or is kind-only): forward upstream
	}
	n := strings.TrimSpace(strings.ToLower(p.BackendName()))
	if n == "" || f.name == "" || n != f.name {
		return false
	}
	if f.kind == "" {
		return true
	}
	return providerKindMatches(p, f.kind)
}

// FilterBackendsByNames keeps backends matching any token in want (case-insensitive).
// Tokens without ":" match BackendName() across all kinds (legacy --backends / URL behavior).
// Tokens with "kind:name" match a single YAML backend kind and name (e.g. truenas:prod, kubernetes:prod).
// Unnamed backends (BackendName() empty) are excluded when want is non-empty.
// When want is empty, provs is returned unchanged.
//
// Honey proxies are name-aware: a token naming a specific honey backend selects
// only that proxy, and a token naming another local backend does not drag other
// honey proxies in. A honey proxy stays transparent (runs to forward the query
// upstream) only for tokens that name nothing configured locally.
func FilterBackendsByNames(provs []Backend, want []string) []Backend {
	if len(want) == 0 {
		return provs
	}
	filters := make([]backendFilter, 0, len(want))
	for _, w := range want {
		filters = append(filters, parseBackendFilter(w))
	}
	localNames := make(map[string]struct{})
	for _, p := range provs {
		if n := strings.TrimSpace(strings.ToLower(p.BackendName())); n != "" {
			localNames[n] = struct{}{}
		}
	}
	var out []Backend
	for _, p := range provs {
		for _, f := range filters {
			if backendMatchesFilter(p, f, localNames) {
				out = append(out, p)
				break
			}
		}
	}
	return out
}
