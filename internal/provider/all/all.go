// Package all provides all native honey provider factories.
package all

import (
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/provider/awsprovider"
	"github.com/shareed2k/honey/internal/provider/consulprovider"
	"github.com/shareed2k/honey/internal/provider/dockerprovider"
	"github.com/shareed2k/honey/internal/provider/gcp"
	"github.com/shareed2k/honey/internal/provider/honeyprovider"
	"github.com/shareed2k/honey/internal/provider/k8sprovider"
	"github.com/shareed2k/honey/internal/provider/localprovider"
	"github.com/shareed2k/honey/internal/provider/proxmoxprovider"
	"github.com/shareed2k/honey/internal/provider/truenasprovider"
	"github.com/shareed2k/honey/internal/searchrun"
	_ "github.com/shareed2k/honey/internal/sshclient" // Registers ssh defaults
)

// Deps carries the ui-implemented runners injected into the providers that need
// to call back into interactive/tunnel session handling. They are supplied by the
// composition root (internal/cli) to keep provider packages leaf-level.
type Deps struct {
	K8sInteractive    k8sprovider.InteractiveRunner
	DockerInteractive dockerprovider.InteractiveRunner
	TruenasTunnel     truenasprovider.TunnelRunner
	TruenasDialer     truenasprovider.UpstreamDialer
}

type configAdapter struct{}

func (c configAdapter) AWSBackends() []config.AWSBackend { return config.Get().Backends.AWS }
func (c configAdapter) AWSBackendSlicePtr() *[]config.AWSBackend {
	cfg := config.Get()
	return &cfg.Backends.AWS
}

func (c configAdapter) SetAWSBackends(b []config.AWSBackend) {
	cfg := config.Get()
	cfg.Backends.AWS = b
}

func (c configAdapter) ConsulBackends() []config.ConsulBackend { return config.Get().Backends.Consul }

func (c configAdapter) ConsulBackendSlicePtr() *[]config.ConsulBackend {
	return &config.Get().Backends.Consul
}

func (c configAdapter) SetConsulBackends(b []config.ConsulBackend) { config.Get().Backends.Consul = b }

func (c configAdapter) DockerBackends() []config.DockerBackend { return config.Get().Backends.Docker }

func (c configAdapter) DockerBackendSlicePtr() *[]config.DockerBackend {
	return &config.Get().Backends.Docker
}

func (c configAdapter) SetDockerBackends(b []config.DockerBackend) { config.Get().Backends.Docker = b }

func (c configAdapter) GCPBackends() []config.GCPBackend { return config.Get().Backends.GCP }

func (c configAdapter) GCPBackendSlicePtr() *[]config.GCPBackend { return &config.Get().Backends.GCP }
func (c configAdapter) SetGCPBackends(b []config.GCPBackend)     { config.Get().Backends.GCP = b }

func (c configAdapter) KubernetesBackends() []config.KubernetesBackend {
	return config.Get().Backends.Kubernetes
}

func (c configAdapter) KubernetesBackendSlicePtr() *[]config.KubernetesBackend {
	return &config.Get().Backends.Kubernetes
}

func (c configAdapter) SetKubernetesBackends(b []config.KubernetesBackend) {
	config.Get().Backends.Kubernetes = b
}
func (c configAdapter) K8sMode() string       { return config.Get().Defaults.K8sMode }
func (c configAdapter) K8sDebugImage() string { return config.Get().Defaults.K8sDebugImage }

func (c configAdapter) LocalBackends() []config.LocalBackend { return config.Get().Backends.Local }
func (c configAdapter) LocalBackendSlicePtr() *[]config.LocalBackend {
	return &config.Get().Backends.Local
}
func (c configAdapter) SetLocalBackends(b []config.LocalBackend) { config.Get().Backends.Local = b }

func (c configAdapter) ProxmoxBackends() []config.ProxmoxBackend {
	return config.Get().Backends.Proxmox
}

func (c configAdapter) ProxmoxBackendSlicePtr() *[]config.ProxmoxBackend {
	return &config.Get().Backends.Proxmox
}

func (c configAdapter) SetProxmoxBackends(b []config.ProxmoxBackend) {
	config.Get().Backends.Proxmox = b
}

func (c configAdapter) TrueNASBackends() []config.TrueNASBackend {
	return config.Get().Backends.TrueNAS
}

func (c configAdapter) TrueNASBackendSlicePtr() *[]config.TrueNASBackend {
	return &config.Get().Backends.TrueNAS
}

func (c configAdapter) SetTrueNASBackends(b []config.TrueNASBackend) {
	config.Get().Backends.TrueNAS = b
}

func (c configAdapter) HoneyBackends() []config.HoneyBackend {
	return config.Get().Backends.Honey
}

func (c configAdapter) HoneyBackendSlicePtr() *[]config.HoneyBackend {
	return &config.Get().Backends.Honey
}

func (c configAdapter) SetHoneyBackends(b []config.HoneyBackend) {
	config.Get().Backends.Honey = b
}

func (c configAdapter) DockerDiscover() config.DockerDiscover {
	return config.Get().Defaults.DockerDiscover
}

// Factories returns a slice of all built-in provider factories, wiring deps into
// the providers that need them.
func Factories(deps Deps) []searchrun.ProviderFactory {
	adapter := configAdapter{}
	return []searchrun.ProviderFactory{
		awsprovider.NewFactory(adapter),
		consulprovider.NewFactory(adapter),
		dockerprovider.NewFactory(deps.DockerInteractive, adapter),
		gcp.NewFactory(adapter),
		honeyprovider.NewFactory(adapter),
		k8sprovider.NewFactory(deps.K8sInteractive, adapter),
		localprovider.NewFactory(adapter),
		proxmoxprovider.NewFactory(adapter),
		truenasprovider.NewFactory(deps.TruenasTunnel, deps.TruenasDialer, adapter),
	}
}
