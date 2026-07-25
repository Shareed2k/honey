package honeyprovider

import (
	"testing"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

// honeyFactory must satisfy ExecutorProvider so ResolveExecutor consults it.
var _ searchrun.ExecutorProvider = honeyFactory{}

type fakeHoneyCfg struct{ backends []config.HoneyBackend }

func (f fakeHoneyCfg) HoneyBackends() []config.HoneyBackend         { return f.backends }
func (f fakeHoneyCfg) HoneyBackendSlicePtr() *[]config.HoneyBackend { return &f.backends }
func (f fakeHoneyCfg) SetHoneyBackends(_ []config.HoneyBackend)     {}

// TestHoneyFactory_HandlesRecord: honeyFactory claims a record only when the
// upstream-routing tag names a honey backend configured on THIS node (client),
// and declines otherwise (e.g. the upstream server, which has no such backend) so
// resolution falls through to the native factory.
func TestHoneyFactory_HandlesRecord(t *testing.T) {
	f := honeyFactory{cfg: fakeHoneyCfg{backends: []config.HoneyBackend{{Name: "remote-builder"}}}}

	rec := func(tag string) hosts.Record {
		return hosts.Record{Provider: "docker", Meta: map[string]string{"kind": "container", "honey_upstream_backend": tag}}
	}

	if !f.HandlesRecord(rec("remote-builder")) {
		t.Fatal("want true: tag names a configured honey backend (client proxies)")
	}
	if f.HandlesRecord(rec("nonexistent")) {
		t.Fatal("want false: tag names an unknown backend (this node cannot proxy)")
	}
	if f.HandlesRecord(rec("")) {
		t.Fatal("want false: no upstream-routing tag")
	}
}
