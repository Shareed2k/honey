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

// FilterBackendsByNames keeps only backends whose BackendName() matches one of want (case-insensitive).
// Unnamed backends (BackendName() empty) are excluded when want is non-empty.
// When want is empty, provs is returned unchanged.
func FilterBackendsByNames(provs []Backend, want []string) []Backend {
	if len(want) == 0 {
		return provs
	}
	set := make(map[string]struct{}, len(want))
	for _, w := range want {
		set[w] = struct{}{}
	}
	var out []Backend
	for _, p := range provs {
		n := strings.TrimSpace(strings.ToLower(p.BackendName()))
		if n == "" {
			continue
		}
		if _, ok := set[n]; ok {
			out = append(out, p)
		}
	}
	return out
}
