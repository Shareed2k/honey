package ui

import (
	"context"
	"io"
	"net"
	"strings"

	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/proxy"
	"github.com/shareed2k/honey/internal/sshclient"
)

// ResolveAppDialer returns the correct proxy.Dialer and an optional io.Closer for any hosts.Record.
// It hides all provider-specific connection logic (SSH, K8s exec, TrueNAS shell API, etc.).
func ResolveAppDialer(ctx context.Context, user string, rec hosts.Record, upstream string) (proxy.Dialer, io.Closer, error) {
	ip := strings.TrimSpace(rec.PrimaryIP)

	// TrueNAS and k8s pods use in-memory API Tunnel dialing. Traditional hosts with IP use SSH.
	useSSH := ip != "" && (rec.Provider != "k8s" || rec.Meta["kind"] != "pod") && rec.Provider != "truenas"

	if !useSSH {
		var executor hostexec.Executor
		if rec.Provider == "truenas" {
			executor = hostexec.TrueNASAPIShellExecutor()
		} else {
			executor = hostexec.ForRecord(rec)
		}

		dialFn := func(ctx context.Context, _, address string) (net.Conn, error) {
			return executor.DialUpstream(ctx, user, rec, address)
		}

		return proxy.NewTunnelDialer(dialFn), nil, nil
	}

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
