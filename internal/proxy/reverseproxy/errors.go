// Package reverseproxy provides a Teleport-inspired HTTP reverse proxy.
package reverseproxy

import (
	"net/http"

	"go.uber.org/zap"
)

// NewErrorHandler returns a handler for proxy errors.
func NewErrorHandler() func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		zap.L().Debug(
			"HTTP app reverse proxy error",
			zap.String("method", r.Method),
			zap.String("host", r.Host),
			zap.String("url", r.URL.String()),
			zap.Error(err),
		)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("Bad Gateway"))
	}
}
