package reverseproxy

import (
	"net/http"
)

// HeaderRewriter rewrites headers for the request.
type HeaderRewriter struct {
	TrustForwardHeaders bool
}

// Rewrite rewrites request headers.
func (r *HeaderRewriter) Rewrite(req *http.Request) {
	if !r.TrustForwardHeaders {
		req.Header.Del("X-Forwarded-For")
		req.Header.Del("X-Forwarded-Proto")
		req.Header.Del("X-Forwarded-Host")
		req.Header.Del("X-Real-IP")
	}
}
