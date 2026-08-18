package webserver

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInterceptPaneMuxName_Stable proves the resume-path mux name is a pure,
// deterministic function of cluster/namespace/pod: unchanged across calls
// with the same inputs, distinct per pod, and always honey-int- prefixed so a
// later task (the tmux registry) can list active panes by that prefix.
func TestInterceptPaneMuxName_Stable(t *testing.T) {
	a := interceptPaneMuxName("stg2", "argocd", "api-0")
	b := interceptPaneMuxName("stg2", "argocd", "api-0")
	require.Equal(t, a, b)
	require.NotEqual(t, a, interceptPaneMuxName("stg2", "argocd", "api-1"))
	require.NotEqual(t, a, interceptPaneMuxName("stg2", "other-ns", "api-0"))
	require.NotEqual(t, a, interceptPaneMuxName("other-cluster", "argocd", "api-0"))
	require.True(t, strings.HasPrefix(a, "honey-int-"), "got %q", a)
}

// TestPtyProxyExecArgs_Subcommand proves ptyProxyExecArgs builds the
// intercept-pane pane argv in order: binary, subcommand, --config <path>,
// then the base64 payload last.
func TestPtyProxyExecArgs_Subcommand(t *testing.T) {
	args := ptyProxyExecArgs("intercept-pane", "/usr/local/bin/honey", "/etc/honey/config.yaml", "BASE64PAYLOAD==")
	require.Equal(t, []string{
		"/usr/local/bin/honey", "intercept-pane", "--config", "/etc/honey/config.yaml", "BASE64PAYLOAD==",
	}, args)
}

// TestPtyProxyExecArgs_PtyProxyUnchanged proves the existing pty-proxy pane
// argv shape survives the sub-parameter generalization, including the
// no-config-path case (no --config pair emitted).
func TestPtyProxyExecArgs_PtyProxyUnchanged(t *testing.T) {
	args := ptyProxyExecArgs("pty-proxy", "/usr/local/bin/honey", "", "BASE64PAYLOAD==")
	require.Equal(t, []string{"/usr/local/bin/honey", "pty-proxy", "BASE64PAYLOAD=="}, args)
}
