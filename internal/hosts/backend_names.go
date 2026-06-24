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

func backendMatchesFilter(p Backend, f backendFilter) bool {
	if p.ID() == "honey" {
		return true // Honey providers act as transparent proxies
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
func FilterBackendsByNames(provs []Backend, want []string) []Backend {
	if len(want) == 0 {
		return provs
	}
	filters := make([]backendFilter, 0, len(want))
	for _, w := range want {
		filters = append(filters, parseBackendFilter(w))
	}
	var out []Backend
	for _, p := range provs {
		for _, f := range filters {
			if backendMatchesFilter(p, f) {
				out = append(out, p)
				break
			}
		}
	}
	return out
}
