// Package k8sproxy implements a per-cluster reverse proxy that forwards
// incoming Kubernetes API requests to a cluster's real API server using the
// cluster's own credentials, impersonating a specified honey identity.
package k8sproxy

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"k8s.io/client-go/rest"
)

// Identity is the Kubernetes identity honey impersonates for a request.
type Identity struct {
	User   string
	Groups []string
}

// clusterProxy reverse-proxies to one cluster's API server.
type clusterProxy struct {
	target *url.URL
	rp     *httputil.ReverseProxy
}

// newClusterProxy builds a reverse proxy to cfg.Host using cfg's transport
// (rest.TransportFor — this carries the cluster CA + the service-account
// credentials honey authenticates to the API server with).
func newClusterProxy(cfg *rest.Config) (*clusterProxy, error) {
	transport, err := rest.TransportFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("k8sproxy: build transport for cluster: %w", err)
	}

	target, err := url.Parse(cfg.Host)
	if err != nil {
		return nil, fmt.Errorf("k8sproxy: parse cluster host: %w", err)
	}

	rp := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = target.Scheme
			pr.Out.URL.Host = target.Host
			pr.Out.Host = target.Host
		},
		// Immediate flushing so kubectl logs -f / exec / port-forward streams
		// promptly instead of waiting on the default periodic flush.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "bad gateway", http.StatusBadGateway)
		},
	}

	return &clusterProxy{target: target, rp: rp}, nil
}

// serve forwards r to the API server as ident. It FIRST removes any
// client-supplied impersonation/authorization headers, then sets honey's own,
// so a client cannot smuggle Impersonate-User: cluster-admin.
func (p *clusterProxy) serve(w http.ResponseWriter, r *http.Request, ident Identity) {
	r.Header.Del("Authorization")

	var impersonationHeaders []string
	for name := range r.Header {
		if strings.HasPrefix(http.CanonicalHeaderKey(name), "Impersonate-") {
			impersonationHeaders = append(impersonationHeaders, name)
		}
	}
	for _, name := range impersonationHeaders {
		r.Header.Del(name)
	}

	r.Header.Set("Impersonate-User", ident.User)
	for _, group := range ident.Groups {
		r.Header.Add("Impersonate-Group", group)
	}

	p.rp.ServeHTTP(w, r)
}
