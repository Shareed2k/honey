// Package reverseproxy provides an HTTP reverse proxy.
package reverseproxy

import (
	"net/http"
	"strings"

	"github.com/shareed2k/honey/internal/apps"
)

// CORSHandler handles CORS preflight requests.
func CORSHandler(cfg *apps.CORSConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "OPTIONS" {
				// Handle preflight
				w.Header().Set("Access-Control-Allow-Origin", strings.Join(cfg.AllowedOrigins, ","))
				w.Header().Set("Access-Control-Allow-Methods", strings.Join(cfg.AllowedMethods, ","))
				w.Header().Set("Access-Control-Allow-Headers", strings.Join(cfg.AllowedHeaders, ","))
				if cfg.AllowCredentials {
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}
				w.WriteHeader(http.StatusOK)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
