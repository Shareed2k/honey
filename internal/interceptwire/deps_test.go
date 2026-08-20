package interceptwire

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	"github.com/shareed2k/honey/internal/config"
)

func TestBuildDeps_UsesInterceptPolicyDir(t *testing.T) {
	cfg := &config.File{Intercept: &config.InterceptConfig{Enabled: true, PolicyDir: ""}}
	deps, sink, err := BuildDeps(context.Background(), cfg, &rest.Config{Host: "https://x"}, fake.NewSimpleClientset(), "ns", "pod", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })
	require.NotNil(t, deps.Enforcer) // embedded default-allow policy loaded
	require.NotNil(t, deps.PortForwarder)
	require.NotNil(t, deps.PodExecer)
	require.NotNil(t, deps.K8sClient)
	require.NotNil(t, deps.LocalRunner)
	require.NotNil(t, sink)
}
