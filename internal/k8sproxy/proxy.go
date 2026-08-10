// Package k8sproxy implements a per-cluster reverse proxy that forwards
// incoming Kubernetes API requests to a cluster's real API server using the
// cluster's own credentials, impersonating a specified honey identity.
package k8sproxy

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

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
	transport, err := upstreamTransport(cfg)
	if err != nil {
		return nil, err
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

// upstreamTransport builds the RoundTripper honey uses to reach the cluster's
// API server. It carries the cluster CA + service-account credentials from cfg
// (via rest.TLSConfigFor + rest.HTTPWrappersForConfig, so no auth material is
// hand-rolled) but is pinned to HTTP/1.1.
//
// HTTP Upgrade — the mechanism SPDY `kubectl exec` / `port-forward` and
// streaming `logs -f` ride on — exists only in HTTP/1.1; there is no HTTP/2
// equivalent. rest.TransportFor negotiates HTTP/2 with the API server by
// default, which makes httputil.ReverseProxy's Connection: Upgrade passthrough
// fail (the backend cannot express the upgrade over h2) and surface as a 502.
// Pinning ALPN to http/1.1 and disabling ForceAttemptHTTP2 keeps exec / logs -f
// / port-forward streaming through end-to-end, while ordinary REST calls are
// unaffected.
func upstreamTransport(cfg *rest.Config) (http.RoundTripper, error) {
	tlsConfig, err := rest.TLSConfigFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("k8sproxy: build cluster TLS config: %w", err)
	}
	if tlsConfig != nil {
		tlsConfig.NextProtos = []string{"http/1.1"}
	}

	base := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSClientConfig:       tlsConfig,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}

	rt, err := rest.HTTPWrappersForConfig(cfg, base)
	if err != nil {
		return nil, fmt.Errorf("k8sproxy: wrap cluster transport: %w", err)
	}
	return rt, nil
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
