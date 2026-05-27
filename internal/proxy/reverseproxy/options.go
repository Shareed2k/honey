package reverseproxy

import (
	"net/http"
)

// Option configures the Forwarder.
type Option func(*Forwarder)

// WithRoundTripper sets the RoundTripper for the Forwarder.
func WithRoundTripper(rt http.RoundTripper) Option {
	return func(f *Forwarder) {
		f.Transport = rt
	}
}

// WithPassHostHeader sets whether to pass the incoming Host header to the upstream.
func WithPassHostHeader(pass bool) Option {
	return func(f *Forwarder) {
		f.PassHostHeader = pass
	}
}

// WithHeaders sets the static headers to be injected into requests.
func WithHeaders(headers map[string]string) Option {
	return func(f *Forwarder) {
		f.Headers = headers
	}
}

// WithResponseModifier sets the modifier for the response.
func WithResponseModifier(fn func(*http.Response) error) Option {
	return func(f *Forwarder) {
		f.ModifyResponse = fn
	}
}
