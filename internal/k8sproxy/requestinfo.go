package k8sproxy

import (
	"net/http"
	"net/url"
	"strings"
)

// RequestInfo is a best-effort description of what a Kubernetes API request
// targets, derived from the request line alone (no server-side discovery).
// It is intentionally lighter than k8s.io/apiserver's RequestInfo: honey only
// needs enough to log/gate requests, not to fully resolve them.
type RequestInfo struct {
	// Verb is one of get|list|create|update|patch|delete|watch|... derived
	// from the HTTP method (and, for GET, whether a resource name is present
	// or ?watch=true is set).
	Verb string
	// APIGroup is "" for the core group (/api/v1/...) or the group name for
	// /apis/<group>/<version>/....
	APIGroup string
	// APIVersion is the version segment, e.g. "v1".
	APIVersion string
	// Resource is the resource type, e.g. "pods", "deployments".
	Resource string
	// Namespace is the namespace segment, empty for cluster-scoped resources.
	Namespace string
	// Name is the resource name, empty for collection requests.
	Name string
	// Subresource is e.g. "exec", "log", "portforward", "status".
	Subresource string
}

// parseRequestInfo derives a RequestInfo from an inbound request's method,
// path, and raw query. It never panics: unknown or opaque paths yield a
// RequestInfo with whatever fields were parseable (typically just Verb).
func parseRequestInfo(method, rawPath, rawQuery string) RequestInfo {
	info := RequestInfo{Verb: verbForMethod(method)}

	segments := splitPath(rawPath)

	var rest []string
	switch {
	case len(segments) >= 2 && segments[0] == "api":
		// /api/<version>/... (core group)
		info.APIVersion = segments[1]
		rest = segments[2:]
	case len(segments) >= 3 && segments[0] == "apis":
		// /apis/<group>/<version>/...
		info.APIGroup = segments[1]
		info.APIVersion = segments[2]
		rest = segments[3:]
	default:
		// Unrecognized/opaque path (e.g. /healthz, /openapi/v2): nothing more
		// to parse.
		return finalizeVerb(info, method, rawQuery)
	}

	if len(rest) > 0 && rest[0] == "namespaces" {
		// namespaces/<ns>[/<resource>[/<name>[/<subresource>]]]
		if len(rest) >= 2 {
			info.Namespace = rest[1]
		}
		rest = rest[min(2, len(rest)):]
	}

	// rest is now <resource>[/<name>[/<subresource>]], whether cluster-scoped
	// or after stripping the namespaces/<ns> prefix.
	if len(rest) >= 1 {
		info.Resource = rest[0]
	}
	if len(rest) >= 2 {
		info.Name = rest[1]
	}
	if len(rest) >= 3 {
		info.Subresource = rest[2]
	}

	return finalizeVerb(info, method, rawQuery)
}

// finalizeVerb refines the method-derived verb using what parsing learned:
// a GET on a collection (no resource name) is "list" rather than "get", and
// ?watch=true on a GET/list overrides both to "watch".
func finalizeVerb(info RequestInfo, method, rawQuery string) RequestInfo {
	if strings.EqualFold(method, http.MethodGet) {
		if info.Name == "" {
			info.Verb = "list"
		} else {
			info.Verb = "get"
		}
	}

	if isWatch(rawQuery) {
		info.Verb = "watch"
	}

	return info
}

// verbForMethod maps an HTTP method to its default Kubernetes verb. GET's
// get-vs-list distinction and any ?watch=true override are applied afterward
// by finalizeVerb, once the path has been parsed.
func verbForMethod(method string) string {
	switch strings.ToUpper(method) {
	case http.MethodGet:
		return "get"
	case http.MethodPost:
		return "create"
	case http.MethodPut:
		return "update"
	case http.MethodPatch:
		return "patch"
	case http.MethodDelete:
		return "delete"
	default:
		return strings.ToLower(method)
	}
}

// isWatch reports whether rawQuery requests a watch (?watch=true, matching
// how kubectl/client-go set it; any other watch= value is not a watch).
func isWatch(rawQuery string) bool {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return false
	}
	return values.Get("watch") == "true"
}

// splitPath splits a URL path into non-empty segments, ignoring leading,
// trailing, and repeated slashes.
func splitPath(p string) []string {
	parts := strings.Split(p, "/")
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			segments = append(segments, part)
		}
	}
	return segments
}
