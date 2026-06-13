package webserver

import (
	"net/http"
	"strings"
)

// subdomainProxyWrapper intercepts requests at the highest level of the HTTP server.
// If the request's Host matches <app-name>.localhost or <app-name>.<host>,
// it routes the request directly to the proxy handler, bypassing the standard mux.
// It also sets a cookie if the ?token= URL parameter is provided.
func (s *Server) subdomainProxyWrapper(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := hostOnly(r.Host)
		if host == "" {
			next.ServeHTTP(w, r)
			return
		}

		parts := strings.SplitN(host, ".", 2)
		if len(parts) < 2 {
			next.ServeHTTP(w, r)
			return
		}

		// Use the first part of the domain as the app name
		appName := parts[0]

		// Attempt to look up the proxy session
		sess := s.proxy.GetLocalSessionByApp(appName)
		if sess == nil || sess.Handler == nil {
			next.ServeHTTP(w, r)
			return
		}

		// The token from the URL query or header
		if r.URL.Query().Get("token") != "" && tokenFromRequest(r, s.opts.Token) {
			// Set the cookie for future requests
			setTokenCookie(w, r, r.URL.Query().Get("token"))

			// Strip the token from the URL to prevent the upstream app from seeing it
			q := r.URL.Query()
			q.Del("token")
			r.URL.RawQuery = q.Encode()

			// Redirect to the same URL without the token
			newURL := r.URL.String()
			if newURL == "" {
				newURL = "/"
			}
			// #nosec G710 -- newURL is strictly built from the current incoming request URL which is safe for local domain routing
			http.Redirect(w, r, newURL, http.StatusTemporaryRedirect)
			return
		}

		// Enforce authentication via the standard flow (which now checks the cookie)
		if !s.authorized(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		// Route directly to the upstream proxy
		sess.Handler.ServeHTTP(w, r)
	})
}
