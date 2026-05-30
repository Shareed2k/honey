package ui

import (
	"context"
	"fmt"
	"io"
	"net"

	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/truenasprovider"
	"github.com/shareed2k/honey/internal/proxy"
	"github.com/shareed2k/honey/internal/sshclient"
)

// AppDialerTransport describes the transport used to reach an app upstream.
type AppDialerTransport string

const (
	// AppDialerTransportSSH means the upstream is reached through a regular SSH client.
	AppDialerTransportSSH AppDialerTransport = "ssh"
	// AppDialerTransportInMemory means the upstream is reached through a provider-native tunnel.
	AppDialerTransportInMemory AppDialerTransport = "in-memory"
)

// TransportForAppDialer returns the transport family used for a record's app upstream connection.
func TransportForAppDialer(rec hosts.Record) AppDialerTransport {
	if appDialerUsesSSH(rec) {
		return AppDialerTransportSSH
	}
	return AppDialerTransportInMemory
}

// ResolveAppDialer returns the correct proxy.Dialer and an optional io.Closer for any hosts.Record.
// It hides all provider-specific connection logic (SSH, K8s exec, TrueNAS shell API, etc.).
func ResolveAppDialer(_ context.Context, user string, rec hosts.Record, _ string) (proxy.Dialer, io.Closer, error) {
	return ResolveAppDialerWithCache(user, rec, nil)
}

// ResolveAppDialerWithCache returns an app proxy dialer, borrowing SSH clients from cache when available.
func ResolveAppDialerWithCache(user string, rec hosts.Record, cache *ClientCache) (proxy.Dialer, io.Closer, error) {
	if !appDialerUsesSSH(rec) {
		var executor hostexec.Executor
		if rec.Provider == "truenas" {
			executor = truenasprovider.APIShellExecutor()
		} else {
			executor = hostexec.ForRecord(rec)
		}

		dialFn := func(ctx context.Context, _, address string) (net.Conn, error) {
			return executor.DialUpstream(ctx, user, rec, address)
		}

		return proxy.NewTunnelDialer(dialFn), nil, nil
	}

	if cache != nil {
		lease, err := cache.AcquireLease(user, rec)
		if err != nil {
			return nil, nil, err
		}
		client, ok := lease.HostClient().(*sshclient.HoneyClient)
		if !ok {
			_ = lease.Close()
			return nil, nil, fmt.Errorf("app proxy SSH transport requires HoneyClient, got %T", lease.HostClient())
		}
		return &proxy.SSHDialer{Client: client}, lease, nil
	}

	ip := hosts.PrimaryIPTrimmed(rec)
	sshPort := 0
	if p, ok := hosts.MetaSSHPort(&rec); ok {
		sshPort = p
	}
	identity := ""
	if id, ok := hosts.MetaSSHIdentityFile(&rec); ok {
		identity = id
	}

	client, err := sshclient.DialHoneyClient(user, ip, sshPort, identity)
	if err != nil {
		return nil, nil, err
	}
	return &proxy.SSHDialer{Client: client}, client, nil
}

func appDialerUsesSSH(rec hosts.Record) bool {
	ip := hosts.PrimaryIPTrimmed(rec)
	return ip != "" && (rec.Provider != "k8s" || rec.Meta["kind"] != "pod") && rec.Provider != "truenas"
}
