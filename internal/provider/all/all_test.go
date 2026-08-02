package all

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/config"
)

func TestConfigAdapter_AWS(t *testing.T) {
	// Initialize a fresh config
	config.Set(&config.File{})
	adapter := configAdapter{}

	// Test Set and Get
	backends := []config.AWSBackend{{Name: "test-aws"}}
	adapter.SetAWSBackends(backends)
	assert.Equal(t, backends, adapter.AWSBackends())

	// Test Ptr
	ptr := adapter.AWSBackendSlicePtr()
	require.NotNil(t, ptr)
	assert.Equal(t, "test-aws", (*ptr)[0].Name)
}

func TestConfigAdapter_Consul(t *testing.T) {
	config.Set(&config.File{})
	adapter := configAdapter{}

	backends := []config.ConsulBackend{{Name: "test-consul"}}
	adapter.SetConsulBackends(backends)
	assert.Equal(t, backends, adapter.ConsulBackends())

	ptr := adapter.ConsulBackendSlicePtr()
	require.NotNil(t, ptr)
	assert.Equal(t, "test-consul", (*ptr)[0].Name)
}

func TestConfigAdapter_Docker(t *testing.T) {
	config.Set(&config.File{})
	adapter := configAdapter{}

	backends := []config.DockerBackend{{Name: "test-docker"}}
	adapter.SetDockerBackends(backends)
	assert.Equal(t, backends, adapter.DockerBackends())

	ptr := adapter.DockerBackendSlicePtr()
	require.NotNil(t, ptr)
	assert.Equal(t, "test-docker", (*ptr)[0].Name)
}

func TestConfigAdapter_GCP(t *testing.T) {
	config.Set(&config.File{})
	adapter := configAdapter{}

	backends := []config.GCPBackend{{Name: "test-gcp"}}
	adapter.SetGCPBackends(backends)
	assert.Equal(t, backends, adapter.GCPBackends())

	ptr := adapter.GCPBackendSlicePtr()
	require.NotNil(t, ptr)
	assert.Equal(t, "test-gcp", (*ptr)[0].Name)
}

func TestConfigAdapter_Kubernetes(t *testing.T) {
	cfg := &config.File{}
	cfg.Defaults.K8sMode = "exec"
	cfg.Defaults.K8sDebugImage = "busybox"
	config.Set(cfg)
	adapter := configAdapter{}

	backends := []config.KubernetesBackend{{Name: "test-k8s"}}
	adapter.SetKubernetesBackends(backends)
	assert.Equal(t, backends, adapter.KubernetesBackends())

	ptr := adapter.KubernetesBackendSlicePtr()
	require.NotNil(t, ptr)
	assert.Equal(t, "test-k8s", (*ptr)[0].Name)

	assert.Equal(t, "exec", adapter.K8sMode())
	assert.Equal(t, "busybox", adapter.K8sDebugImage())
}

func TestConfigAdapter_Local(t *testing.T) {
	config.Set(&config.File{})
	adapter := configAdapter{}

	backends := []config.LocalBackend{{Name: "test-local"}}
	adapter.SetLocalBackends(backends)
	assert.Equal(t, backends, adapter.LocalBackends())

	ptr := adapter.LocalBackendSlicePtr()
	require.NotNil(t, ptr)
	assert.Equal(t, "test-local", (*ptr)[0].Name)
}

func TestConfigAdapter_Proxmox(t *testing.T) {
	config.Set(&config.File{})
	adapter := configAdapter{}

	backends := []config.ProxmoxBackend{{Name: "test-proxmox"}}
	adapter.SetProxmoxBackends(backends)
	assert.Equal(t, backends, adapter.ProxmoxBackends())

	ptr := adapter.ProxmoxBackendSlicePtr()
	require.NotNil(t, ptr)
	assert.Equal(t, "test-proxmox", (*ptr)[0].Name)
}

func TestConfigAdapter_TrueNAS(t *testing.T) {
	config.Set(&config.File{})
	adapter := configAdapter{}

	backends := []config.TrueNASBackend{{Name: "test-truenas"}}
	adapter.SetTrueNASBackends(backends)
	assert.Equal(t, backends, adapter.TrueNASBackends())

	ptr := adapter.TrueNASBackendSlicePtr()
	require.NotNil(t, ptr)
	assert.Equal(t, "test-truenas", (*ptr)[0].Name)
}

func TestConfigAdapter_Honey(t *testing.T) {
	config.Set(&config.File{})
	adapter := configAdapter{}

	backends := []config.HoneyBackend{{Name: "test-honey"}}
	adapter.SetHoneyBackends(backends)
	assert.Equal(t, backends, adapter.HoneyBackends())

	ptr := adapter.HoneyBackendSlicePtr()
	require.NotNil(t, ptr)
	assert.Equal(t, "test-honey", (*ptr)[0].Name)
}

func TestConfigAdapter_DockerDiscover(t *testing.T) {
	cfg := &config.File{}
	cfg.Defaults.DockerDiscover = config.DockerDiscover{Enabled: true}
	config.Set(cfg)

	adapter := configAdapter{}
	dd := adapter.DockerDiscover()
	assert.True(t, dd.Enabled)
}

func TestFactories(t *testing.T) {
	deps := Deps{}
	factories := Factories(deps)
	// We expect 9 built-in factories (AWS, Consul, Docker, GCP, Honey, K8s, Local, Proxmox, TrueNAS)
	assert.Len(t, factories, 9)
}
