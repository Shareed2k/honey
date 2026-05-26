package reverseproxy

import (
	"context"
	"net/http"
	"net/http/httputil"
	"net/url"
)

type contextKey string

const originalHostKey contextKey = "originalHost"

// Forwarder wraps httputil.ReverseProxy.
type Forwarder struct {
	*httputil.ReverseProxy
	PassHostHeader bool
	Headers        map[string]string
}

// New creates a new Forwarder.
func New(target *url.URL, opts ...Option) *Forwarder {
	f := &Forwarder{
		ReverseProxy: httputil.NewSingleHostReverseProxy(target),
	}

	// We must clear Director because httputil.NewSingleHostReverseProxy sets it.
	f.Director = nil

	f.Rewrite = func(req *httputil.ProxyRequest) {
		req.SetURL(target)

		// Context-based original host propagation
		ctx := context.WithValue(req.Out.Context(), originalHostKey, req.In.Host)
		req.Out = req.Out.WithContext(ctx)

		rewriter := &HeaderRewriter{TrustForwardHeaders: false}
		rewriter.Rewrite(req.Out)

		// Set proper X-Forwarded headers
		req.Out.Header.Set("X-Forwarded-Host", req.In.Host)
		if req.In.TLS != nil {
			req.Out.Header.Set("X-Forwarded-Proto", "https")
		} else {
			req.Out.Header.Set("X-Forwarded-Proto", "http")
		}

		// Apply custom headers
		for k, v := range f.Headers {
			req.Out.Header.Set(k, v)
		}

		if !f.PassHostHeader {
			req.Out.Host = target.Host
		}
	}

	// Automatically rewrite Redirects / Location headers
	f.ModifyResponse = func(res *http.Response) error {
		origHost, ok := res.Request.Context().Value(originalHostKey).(string)
		if !ok || origHost == "" {
			return nil
		}

		loc := res.Header.Get("Location")
		if loc != "" {
			u, err := url.Parse(loc)
			if err == nil {
				// If the redirect is relative or explicitly references the upstream host, rewrite it to keep the client on the proxy subdomain.
				if u.Host == target.Host || u.Host == "" {
					u.Scheme = res.Request.URL.Scheme
					if u.Scheme == "" {
						u.Scheme = "http"
					}
					u.Host = origHost
					res.Header.Set("Location", u.String())
				}
			}
		}
		return nil
	}

	for _, opt := range opts {
		opt(f)
	}

	return f
}
