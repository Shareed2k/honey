package k8sprovider

import (
	"testing"

	"github.com/shareed2k/honey/internal/hostexec"
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
		// Upstream-tagged pod: honey (ordered first, when it has the backend) claims
		// it for proxying; on the upstream server honey declines and k8s resolves the
		// pod locally, so k8s claims by kind regardless of the tag.
		{"upstream tag still claimed by kind", hosts.Record{Provider: "k8s", Meta: map[string]string{"kind": "pod", "honey_upstream_backend": "remote-builder"}}, true},
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

// The executor returned for a pod must satisfy hostexec.InteractiveStreamer so
// RunK8sPodWebTTY can route through the seam (Registry.ForRecord + type assert)
// instead of down-casting to *K8sPodExecutor. A pod reached over the honey mesh
// resolves to honeyprovider's executor, which also implements the interface.
func TestK8sFactory_ExecutorForPodIsInteractiveStreamer(t *testing.T) {
	f := k8sFactory{}
	pod := hosts.Record{Provider: "k8s", Meta: map[string]string{"kind": "pod", "namespace": "default", "pod_name": "x"}}
	ex := f.ExecutorFor(pod, nil)
	if ex == nil {
		t.Fatalf("ExecutorFor(pod) = nil, want executor")
	}
	if _, ok := ex.(hostexec.InteractiveStreamer); !ok {
		t.Fatalf("ExecutorFor(pod) = %T, does not implement hostexec.InteractiveStreamer", ex)
	}
}
