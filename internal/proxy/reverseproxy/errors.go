// Package reverseproxy provides a Teleport-inspired HTTP reverse proxy.
package reverseproxy

import (
	"net/http"
)

// NewErrorHandler returns a handler for proxy errors.
func NewErrorHandler() func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, _ *http.Request, _ error) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("Bad Gateway"))
	}
}
