package k8sproxy

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/policy"
	"github.com/shareed2k/honey/internal/safepath"
)

// ServerConfig is the fully-resolved configuration for a live k8s access proxy.
// The webserver owns one of these (built by the CLI from config.K8sProxy) and
// hands it to RunServer inside its listener goroutine.
type ServerConfig struct {
	// Listen is the host:port the mTLS listener binds.
	Listen string
	// ServingCertPath / ServingKeyPath point at an operator-supplied serving
	// keypair. When either is empty a self-signed cert is generated under the
	// state dir (see EnsureServingCert).
	ServingCertPath string
	ServingKeyPath  string
	// ClientCAPath is the mTLS client-CA trust anchor. When empty the caller's
	// default (the built-in device CA) is used.
	ClientCAPath string
	// Registry holds the per-cluster proxies + impersonation mappings.
	Registry *Registry
	// Enforcer gates every request through OPA (fail-closed). nil allows.
	Enforcer *policy.Enforcer
	// AuditSink receives one event per request decision. nil becomes a no-op.
	AuditSink audit.Sink
	// SANs are extra subject-alternative names for a generated serving cert.
	SANs []string
}

// RunServer resolves the serving certificate and client-CA trust anchor, builds
// the mTLS serving config + handler, and serves until ctx is cancelled. It fails
// (wrapped) when no client CA is available — the boundary must never come up
// without a trust anchor to verify clients against.
func RunServer(ctx context.Context, cfg ServerConfig, defaultClientCAPEM []byte, stateDir string) error {
	certPEM, keyPEM, err := resolveServingCert(cfg, stateDir)
	if err != nil {
		return err
	}
	clientCAPEM, err := resolveClientCA(cfg, defaultClientCAPEM)
	if err != nil {
		return err
	}
	tlsCfg, err := BuildServerTLSConfig(certPEM, keyPEM, clientCAPEM)
	if err != nil {
		return err
	}
	h := NewHandler(cfg.Registry, cfg.Enforcer, cfg.AuditSink)
	return Serve(ctx, cfg.Listen, tlsCfg, h)
}

// resolveServingCert loads the serving keypair from cfg's explicit paths when
// both are set, else ensures a self-signed one under stateDir.
func resolveServingCert(cfg ServerConfig, stateDir string) (certPEM, keyPEM []byte, err error) {
	certPath := strings.TrimSpace(cfg.ServingCertPath)
	keyPath := strings.TrimSpace(cfg.ServingKeyPath)
	if certPath != "" && keyPath != "" {
		cpem, err := safepath.ReadFile(certPath)
		if err != nil {
			return nil, nil, fmt.Errorf("k8sproxy: read serving cert: %w", err)
		}
		kpem, err := safepath.ReadFile(keyPath)
		if err != nil {
			return nil, nil, fmt.Errorf("k8sproxy: read serving key: %w", err)
		}
		return cpem, kpem, nil
	}
	return EnsureServingCert(stateDir, cfg.SANs)
}

// resolveClientCA reads the client CA from cfg.ClientCAPath when set, else falls
// back to the caller-supplied default. It errors when neither is available.
func resolveClientCA(cfg ServerConfig, defaultClientCAPEM []byte) ([]byte, error) {
	if p := strings.TrimSpace(cfg.ClientCAPath); p != "" {
		pemBytes, err := safepath.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("k8sproxy: read client CA: %w", err)
		}
		return pemBytes, nil
	}
	if len(defaultClientCAPEM) == 0 {
		return nil, fmt.Errorf("k8sproxy: no client CA available (configure client_ca or enable the device CA)")
	}
	return defaultClientCAPEM, nil
}

// ServingCAPath returns the on-disk path of the proxy's self-signed serving
// certificate under stateDir, so a client kubeconfig can trust it (used by K4).
func ServingCAPath(stateDir string) string {
	if p, err := safepath.JoinUnder(stateDir, servingCertFile); err == nil {
		return p
	}
	return filepath.Join(strings.TrimSpace(stateDir), servingCertFile)
}
