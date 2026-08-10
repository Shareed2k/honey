package k8sproxy

import (
	"fmt"
	"net/http"

	"k8s.io/client-go/rest"
)

// ClusterSpec is one cluster the proxy fronts.
type ClusterSpec struct {
	// Name is the path-prefix name kubectl targets (/<Name>/...) and the key
	// used to look the cluster up in the Registry.
	Name string
	// Config is the upstream API-server config (built by the caller, e.g. via
	// k8sprovider.RestConfigForKubeconfig).
	Config *rest.Config
	// UserFrom selects how Impersonate-User is derived from the authenticated
	// actor. "cn" (or empty) uses the actor as-is (the client-cert CN).
	UserFrom string
	// DefaultGroups are the Impersonate-Group values applied to a request when
	// the client certificate carries no groups of its own (its O= fields).
	DefaultGroups []string
	// Labels are arbitrary key/value tags for this cluster, exposed to the OPA
	// k8s_request gate as input.cluster_labels so policy can select clusters by
	// label (e.g. region, platform) rather than only by name.
	Labels map[string]string
}

// clusterEntry pairs a built proxy with the cluster's impersonation mapping.
type clusterEntry struct {
	proxy *clusterProxy
	spec  ClusterSpec
}

// Registry holds the per-cluster proxies the k8s access proxy fronts, plus
// each cluster's impersonation mapping. It is immutable after NewRegistry and
// safe for concurrent use without locking.
type Registry struct {
	clusters map[string]*clusterEntry
}

// NewRegistry builds a clusterProxy for each spec. It errors (wrapped, naming
// the offending cluster) on a duplicate name, an empty name, a nil Config, or
// a newClusterProxy failure.
func NewRegistry(specs []ClusterSpec) (*Registry, error) {
	clusters := make(map[string]*clusterEntry, len(specs))

	for _, spec := range specs {
		if spec.Name == "" {
			return nil, fmt.Errorf("k8sproxy: registry: cluster has empty name")
		}
		if _, exists := clusters[spec.Name]; exists {
			return nil, fmt.Errorf("k8sproxy: registry: duplicate cluster name %q", spec.Name)
		}
		if spec.Config == nil {
			return nil, fmt.Errorf("k8sproxy: registry: cluster %q: nil rest.Config", spec.Name)
		}

		proxy, err := newClusterProxy(spec.Config)
		if err != nil {
			return nil, fmt.Errorf("k8sproxy: registry: cluster %q: %w", spec.Name, err)
		}

		clusters[spec.Name] = &clusterEntry{proxy: proxy, spec: spec}
	}

	return &Registry{clusters: clusters}, nil
}

// Has reports whether cluster is a known cluster name.
func (r *Registry) Has(cluster string) bool {
	_, ok := r.clusters[cluster]
	return ok
}

// LabelsFor returns cluster's configured labels (nil if the cluster is unknown
// or has none). Exposed to the OPA gate as input.cluster_labels.
func (r *Registry) LabelsFor(cluster string) map[string]string {
	entry, ok := r.clusters[cluster]
	if !ok {
		return nil
	}
	return entry.spec.Labels
}

// IdentityFor maps the authenticated actor to the impersonated identity for
// cluster using the cluster's DefaultGroups. Equivalent to
// IdentityForWithGroups(cluster, actor, nil).
func (r *Registry) IdentityFor(cluster, actor string) (Identity, bool) {
	return r.IdentityForWithGroups(cluster, actor, nil)
}

// IdentityForWithGroups maps the authenticated actor to the impersonated
// identity for cluster. For UserFrom "cn" (the default, and the empty value)
// the Impersonate-User is the actor itself. Groups are certGroups (the client
// certificate's O= fields) when non-empty, otherwise the cluster's
// DefaultGroups fallback. ok is false if the cluster is unknown.
func (r *Registry) IdentityForWithGroups(cluster, actor string, certGroups []string) (Identity, bool) {
	entry, ok := r.clusters[cluster]
	if !ok {
		return Identity{}, false
	}

	// UserFrom currently only supports "cn" (client-cert CN, passed in as
	// actor by the caller); other values behave the same for now and are
	// reserved for future derivation modes.
	groups := certGroups
	if len(groups) == 0 {
		groups = entry.spec.DefaultGroups
	}
	return Identity{
		User:   actor,
		Groups: groups,
	}, true
}

// Serve looks cluster's proxy up and forwards w2/r as ident. An unknown
// cluster is a no-op returning false — the caller's handler should 404 via
// Has before ever reaching here — so Serve never panics on a bad name.
func (r *Registry) Serve(w http.ResponseWriter, r2 *http.Request, cluster string, ident Identity) bool {
	entry, ok := r.clusters[cluster]
	if !ok {
		return false
	}
	entry.proxy.serve(w, r2, ident)
	return true
}
