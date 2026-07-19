package dockerprovider

import (
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

// dockerFactory must satisfy ExecutorProvider so ResolveExecutor consults it;
// otherwise docker records fall through to the SSH fallback.
var _ searchrun.ExecutorProvider = dockerFactory{}

func TestDockerFactory_HandlesRecord(t *testing.T) {
	f := dockerFactory{}
	cases := []struct {
		name string
		rec  hosts.Record
		want bool
	}{
		{"container", hosts.Record{Provider: "docker", Meta: map[string]string{"kind": "container", "container_id": "abc"}}, true},
		{"swarm_task", hosts.Record{Provider: "docker", Meta: map[string]string{"kind": "swarm_task"}}, true},
		{"upstream proxied excluded", hosts.Record{Provider: "docker", Meta: map[string]string{"kind": "container", "honey_upstream_backend": "remote-builder"}}, false},
		{"non-container kind", hosts.Record{Provider: "docker", Meta: map[string]string{"kind": "image"}}, false},
		{"no kind", hosts.Record{Provider: "docker", Meta: map[string]string{}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := f.HandlesRecord(tc.rec); got != tc.want {
				t.Fatalf("HandlesRecord(%q) = %v, want %v", tc.name, got, tc.want)
			}
			// HandlesRecord must gate ExecutorFor: when it returns true, an
			// executor is produced; the upstream-proxied case must NOT be
			// claimed here (honeyprovider handles it).
			if tc.want && f.ExecutorFor(tc.rec, nil) == nil {
				t.Fatalf("ExecutorFor(%q) = nil but HandlesRecord = true", tc.name)
			}
		})
	}
}
