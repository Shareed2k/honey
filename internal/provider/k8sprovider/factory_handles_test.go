package k8sprovider

import (
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

// k8sFactory must satisfy ExecutorProvider so ResolveExecutor consults it;
// otherwise pod records fall through to the SSH fallback.
var _ searchrun.ExecutorProvider = k8sFactory{}

func TestK8sFactory_HandlesRecord(t *testing.T) {
	f := k8sFactory{}
	cases := []struct {
		name string
		rec  hosts.Record
		want bool
	}{
		{"pod", hosts.Record{Provider: "k8s", Meta: map[string]string{"kind": "pod", "namespace": "default", "pod_name": "x"}}, true},
		{"upstream proxied excluded", hosts.Record{Provider: "k8s", Meta: map[string]string{"kind": "pod", "honey_upstream_backend": "remote-builder"}}, false},
		{"non-pod kind", hosts.Record{Provider: "k8s", Meta: map[string]string{"kind": "node"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := f.HandlesRecord(tc.rec); got != tc.want {
				t.Fatalf("HandlesRecord(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}
