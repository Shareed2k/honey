package webserver

import (
	"context"
	"io"
	"testing"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/honeyprovider"
)

// fakeUpstreamRegistry is a minimal hostexec.Registry whose ForRecord returns a
// preset executor, for exercising honeyUpstreamExecutorFor's routing decision.
type fakeUpstreamRegistry struct{ ex hostexec.Executor }

func (f fakeUpstreamRegistry) ForRecord(hosts.Record) hostexec.Executor { return f.ex }
func (fakeUpstreamRegistry) Reconfigure(*config.File)                   {}
func (fakeUpstreamRegistry) RunSSHTunnel(context.Context, string, string, int, string, io.Writer) error {
	return nil
}
func (fakeUpstreamRegistry) BorrowSSH(string, hosts.Record) (any, bool) { return nil, false }

func TestHoneyUpstreamExecutorFor(t *testing.T) {
	rec := hosts.Record{Name: "x", Meta: map[string]string{"honey_upstream_backend": "dokploy"}}

	// nil registry -> not proxied.
	if got := honeyUpstreamExecutorFor(nil, rec); got != nil {
		t.Fatalf("nil registry: want nil, got %v", got)
	}

	// This node runs the record locally (registry resolves to a non-honey
	// executor / nil) -> not proxied, so the caller strips the stale meta and
	// dispatches natively.
	if got := honeyUpstreamExecutorFor(fakeUpstreamRegistry{ex: nil}, rec); got != nil {
		t.Fatalf("non-honey executor: want nil, got %v", got)
	}

	// This node routes the record through a honey upstream backend -> return
	// the honeyprovider Executor so the terminal is proxied to it.
	want := &honeyprovider.Executor{URL: "http://mesh-peer/"}
	if got := honeyUpstreamExecutorFor(fakeUpstreamRegistry{ex: want}, rec); got != want {
		t.Fatalf("honey executor: want %p, got %p", want, got)
	}
}
