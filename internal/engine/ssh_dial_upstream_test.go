package engine

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
)

// fakeUpstreamExecutor records whether RunInteractive was invoked, standing in
// for the honeyprovider Executor that proxies interactive sessions upstream.
type fakeUpstreamExecutor struct{ interactiveCalled bool }

func (f *fakeUpstreamExecutor) Dial(string, hosts.Record) (hostexec.HostClient, error) {
	return nil, errors.New("fake: no dial in test")
}

func (f *fakeUpstreamExecutor) RunInteractive(string, hosts.Record) error {
	f.interactiveCalled = true
	return nil
}

func (f *fakeUpstreamExecutor) RunTunnel(context.Context, string, hosts.Record, string, io.Writer) error {
	return nil
}

func (f *fakeUpstreamExecutor) DialUpstream(context.Context, string, hosts.Record, string) (net.Conn, error) {
	return nil, nil
}

type fakeUpstreamRegistry struct{ ex hostexec.Executor }

func (r fakeUpstreamRegistry) ForRecord(hosts.Record) hostexec.Executor { return r.ex }
func (fakeUpstreamRegistry) Reconfigure(*config.File)                   {}
func (fakeUpstreamRegistry) RunSSHTunnel(context.Context, string, string, int, string, io.Writer) error {
	return nil
}
func (fakeUpstreamRegistry) BorrowSSH(string, hosts.Record) (any, bool) { return nil, false }

// TestRunTerminalInteractive_RoutesUpstream verifies that a docker record tagged
// with honey_upstream_backend is delegated to the resolved Executor's
// RunInteractive (the honeyprovider proxy) instead of the local docker path.
func TestRunTerminalInteractive_RoutesUpstream(t *testing.T) {
	fe := &fakeUpstreamExecutor{}
	rec := hosts.Record{
		Name:     "cntr",
		Provider: "docker",
		Meta:     map[string]string{"kind": "docker", "container_id": "abc", "honey_upstream_backend": "dokploy"},
	}

	if err := RunTerminalInteractive("ubuntu", rec, "", fakeUpstreamRegistry{ex: fe}); err != nil {
		t.Fatalf("RunTerminalInteractive: %v", err)
	}
	if !fe.interactiveCalled {
		t.Fatal("expected upstream Executor.RunInteractive to be called for an upstream-tagged record")
	}
}

// TestRunTerminalInteractive_LocalDockerNotRouted verifies the meta gate: a
// docker record WITHOUT the upstream tag is NOT sent to the proxy path and
// instead falls through to the local docker dispatch (which here fails on the
// fake's Dial error, proving it took the native path rather than proxying).
func TestRunTerminalInteractive_LocalDockerNotRouted(t *testing.T) {
	fe := &fakeUpstreamExecutor{}
	rec := hosts.Record{
		Name:     "cntr",
		Provider: "docker",
		Meta:     map[string]string{"kind": "docker", "container_id": "abc"},
	}

	err := RunTerminalInteractive("ubuntu", rec, "", fakeUpstreamRegistry{ex: fe})
	if fe.interactiveCalled {
		t.Fatal("local docker record must not be routed to the upstream proxy")
	}
	if err == nil {
		t.Fatal("expected the local docker dispatch (fake Dial error), got nil")
	}
}
