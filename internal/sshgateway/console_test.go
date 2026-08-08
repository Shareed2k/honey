package sshgateway

import (
	"bytes"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/proxmoxprovider"
	"github.com/shareed2k/honey/internal/provider/truenasprovider"
	"github.com/shareed2k/honey/internal/searchrun"
)

// consoleTestConfigAdapter satisfies both proxmoxprovider.ConfigProvider and
// truenasprovider.ConfigProvider, reading through the process-global config so the
// provider runtime registries (proxmoxprovider.BackendByName /
// truenasprovider.BackendByName) resolve the console records' backends.
type consoleTestConfigAdapter struct{}

func (consoleTestConfigAdapter) ProxmoxBackends() []config.ProxmoxBackend {
	return config.Get().Backends.Proxmox
}

func (consoleTestConfigAdapter) ProxmoxBackendSlicePtr() *[]config.ProxmoxBackend {
	return &config.Get().Backends.Proxmox
}

func (consoleTestConfigAdapter) SetProxmoxBackends(b []config.ProxmoxBackend) {
	config.Get().Backends.Proxmox = b
}

func (consoleTestConfigAdapter) TrueNASBackends() []config.TrueNASBackend {
	return config.Get().Backends.TrueNAS
}

func (consoleTestConfigAdapter) TrueNASBackendSlicePtr() *[]config.TrueNASBackend {
	return &config.Get().Backends.TrueNAS
}

func (consoleTestConfigAdapter) SetTrueNASBackends(b []config.TrueNASBackend) {
	config.Get().Backends.TrueNAS = b
}

func (consoleTestConfigAdapter) DockerDiscover() config.DockerDiscover {
	return config.DockerDiscover{}
}

// registerConsoleBackends populates the Proxmox and TrueNAS runtime registries
// with fake (never dialed) backends so ShouldUsePVETTY / ShouldUseTrueNASShell
// recognize the console records. Restored on test cleanup.
func registerConsoleBackends(t *testing.T) {
	t.Helper()
	config.Set(&config.File{
		Version: 1,
		Backends: config.Backends{
			Proxmox: []config.ProxmoxBackend{{
				Name:        "pve1",
				URL:         "https://pve.example:8006",
				TokenID:     "root@pam!honey",
				TokenSecret: "secret",
				ExecMode:    "pve",
			}},
			TrueNAS: []config.TrueNASBackend{{
				Name:   "nas1",
				URL:    "https://nas.example",
				APIKey: "1-secret",
			}},
		},
	})
	adapter := consoleTestConfigAdapter{}
	reg := searchrun.NewRegistry([]searchrun.ProviderFactory{
		proxmoxprovider.NewFactory(adapter),
		truenasprovider.NewFactory(nil, nil, adapter),
	})
	reg.ReconfigureFromConfig()
	t.Cleanup(func() {
		config.Set(&config.File{})
		reg.ReconfigureFromConfig()
	})
}

// proxmoxConsoleRecord is a Proxmox LXC serial-console record (ShouldUsePVETTY
// true given the registered pve1 backend). PrimaryIP is only for the by-name
// resolver; the console never reaches the network in these tests.
func proxmoxConsoleRecord() hosts.Record {
	return hosts.Record{
		Provider:  "proxmox",
		Name:      "pve-ct",
		PrimaryIP: "10.0.0.50",
		Meta: map[string]string{
			"kind":         "lxc",
			"node":         "node1",
			"vmid":         "105",
			"backend_name": "pve1",
		},
	}
}

// truenasConsoleRecord is a TrueNAS appliance middleware-shell record
// (ShouldUseTrueNASShell true given the registered nas1 backend).
func truenasConsoleRecord() hosts.Record {
	return hosts.Record{
		Provider:  "truenas",
		Name:      "nas-box",
		PrimaryIP: "10.0.0.60",
		Meta: map[string]string{
			"kind":         "appliance",
			"backend_name": "nas1",
		},
	}
}

// TestGateway_ConsoleTargetsRejectExec proves an ad-hoc command against a
// console-only target (Proxmox serial, TrueNAS shell) is refused with an
// actionable message and a deny audit, before any gate or dial. No appliance is
// contacted.
func TestGateway_ConsoleTargetsRejectExec(t *testing.T) {
	sandboxSSHEnv(t)
	registerConsoleBackends(t)

	ca := newEd25519Signer(t)
	sink := &memSink{}

	addr, stopGW := startGateway(t, Options{
		TrustedCAs: []ssh.PublicKey{ca.PublicKey()},
		AuditSink:  sink,
		Records:    staticRecords(proxmoxConsoleRecord(), truenasConsoleRecord()),
	})
	defer stopGW()

	client, err := dialGateway(t, addr, "alice", signedCertAuth(t, ca, "alice", "alice@corp", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	for _, name := range []string{"pve-ct", "nas-box"} {
		t.Run(name, func(t *testing.T) {
			sess, err := client.NewSession()
			if err != nil {
				t.Fatalf("session: %v", err)
			}
			defer func() { _ = sess.Close() }()
			var stderr bytes.Buffer
			sess.Stderr = &stderr
			if err := sess.Run(name + " uptime"); err == nil {
				t.Fatalf("expected exec on console-only target %q to fail", name)
			}
			if !strings.Contains(stderr.String(), "console-only target") {
				t.Fatalf("stderr = %q, want console-only message", stderr.String())
			}
		})
	}

	e, ok := sink.find("command_exec")
	if !ok || e.Decision != "deny" || e.DenyReason != "console-only target" {
		t.Fatalf("command_exec audit = %+v, want deny console-only target", e)
	}
}

// TestGateway_ConsoleTargetRejectsDirectTCPIP proves `ssh -L` to a console-only
// target fails the channel-open (Prohibited) with a deny audit, without dialing.
func TestGateway_ConsoleTargetRejectsDirectTCPIP(t *testing.T) {
	sandboxSSHEnv(t)
	registerConsoleBackends(t)

	ca := newEd25519Signer(t)
	sink := &memSink{}

	addr, stopGW := startGateway(t, Options{
		TrustedCAs: []ssh.PublicKey{ca.PublicKey()},
		AuditSink:  sink,
		Records:    staticRecords(proxmoxConsoleRecord()),
	})
	defer stopGW()

	client, err := dialGateway(t, addr, "alice", signedCertAuth(t, ca, "alice", "alice@corp", time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	fwd, err := client.Dial("tcp", net.JoinHostPort("pve-ct", strconv.Itoa(5900)))
	if err == nil {
		_ = fwd.Close()
		t.Fatalf("expected port-forward to a console-only target to be rejected")
	}

	e, ok := sink.find("tunnel")
	if !ok {
		t.Fatalf("no tunnel audit event")
	}
	if e.Decision != "deny" || e.Target != "pve-ct" {
		t.Fatalf("tunnel audit = %+v, want deny pve-ct", e)
	}
	if !strings.Contains(e.DenyReason, "console-only target") {
		t.Fatalf("deny reason = %q, want console-only", e.DenyReason)
	}
}
