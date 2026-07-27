package webserver

import (
	"context"
	"io"
	"net"
	"testing"

	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/honeyprovider"
)

// localExecutor is a native hostexec.Executor that runs records on this node; it
// deliberately does NOT implement hostexec.ProxyExecutor, so isProxyExecutor must
// treat it as local.
type localExecutor struct{}

func (localExecutor) Dial(string, hosts.Record) (hostexec.HostClient, error) { return nil, nil }
func (localExecutor) RunInteractive(string, hosts.Record) error              { return nil }
func (localExecutor) RunTunnel(context.Context, string, hosts.Record, string, io.Writer) error {
	return nil
}

func (localExecutor) DialUpstream(context.Context, string, hosts.Record, string) (net.Conn, error) {
	return nil, nil
}

// TestIsProxyExecutor verifies the web terminal's local-vs-proxy routing decision:
// a mesh-forwarding executor (honeyprovider) is proxied wholesale, while a nil or
// native executor is handled locally. This is what replaced the honey-upstream
// band-aid: the dispatcher asks the seam "do you forward this elsewhere?" via
// hostexec.ProxyExecutor instead of type-asserting a concrete provider.
func TestIsProxyExecutor(t *testing.T) {
	if isProxyExecutor(nil) {
		t.Fatal("nil executor: want false (local fallback)")
	}
	if isProxyExecutor(localExecutor{}) {
		t.Fatal("native executor: want false (runs locally)")
	}
	if !isProxyExecutor(&honeyprovider.Executor{URL: "http://mesh-peer/"}) {
		t.Fatal("honeyprovider executor: want true (forwarded over the mesh)")
	}
}

// compile-time check: the fake native executor really satisfies hostexec.Executor.
var _ hostexec.Executor = localExecutor{}
