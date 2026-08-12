package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/intercept"
)

func TestParseModes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		in           []string
		wantEgress   bool
		wantIncoming bool
		wantFiles    bool
		wantErr      bool
	}{
		{name: "egress", in: []string{"egress"}, wantEgress: true},
		{name: "incoming", in: []string{"incoming"}, wantIncoming: true},
		{name: "files", in: []string{"files"}, wantFiles: true},
		{name: "all three", in: []string{"egress", "incoming", "files"}, wantEgress: true, wantIncoming: true, wantFiles: true},
		{name: "unknown mode errors", in: []string{"egress", "bogus"}, wantErr: true},
		{name: "empty requires at least one", in: nil, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			modes, err := intercept.ParseModes(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantEgress, modes.Egress)
			assert.Equal(t, tc.wantIncoming, modes.Incoming)
			assert.Equal(t, tc.wantFiles, modes.Files)
		})
	}
}

func TestInterceptCmd_FlagParsing(t *testing.T) {
	t.Parallel()
	cmd := newInterceptCmd()
	require.NoError(t, cmd.ParseFlags([]string{
		"-n", "apps",
		"--container", "app",
		"--mode", "egress,files",
		"--target", "127.0.0.1:8080",
		"--udp",
		"--cluster", "prod",
		"--agent-image", "registry.example/agent:1",
		"--actor", "roman",
	}))

	ns, err := cmd.Flags().GetString("namespace")
	require.NoError(t, err)
	assert.Equal(t, "apps", ns)

	container, err := cmd.Flags().GetString("container")
	require.NoError(t, err)
	assert.Equal(t, "app", container)

	modes, err := cmd.Flags().GetStringSlice("mode")
	require.NoError(t, err)
	assert.Equal(t, []string{"egress", "files"}, modes)

	target, err := cmd.Flags().GetString("target")
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:8080", target)

	udp, err := cmd.Flags().GetBool("udp")
	require.NoError(t, err)
	assert.True(t, udp)

	cluster, err := cmd.Flags().GetString("cluster")
	require.NoError(t, err)
	assert.Equal(t, "prod", cluster)

	image, err := cmd.Flags().GetString("agent-image")
	require.NoError(t, err)
	assert.Equal(t, "registry.example/agent:1", image)
}

// runInterceptArgs drives cobra's own flag/`--` parsing so ArgsLenAtDash is set
// exactly as it is in production, then returns interceptArgs' result.
func runInterceptArgs(t *testing.T, argv []string) (pod string, command []string, err error) {
	t.Helper()
	cmd := &cobra.Command{
		Use:  "intercept",
		Args: cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			pod, command, err = interceptArgs(c, args)
			return nil
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.SetArgs(argv)
	require.NoError(t, cmd.Execute())
	return pod, command, err
}

func TestInterceptArgs_CommandPassthrough(t *testing.T) {
	t.Parallel()

	t.Run("command after dash", func(t *testing.T) {
		t.Parallel()
		pod, command, err := runInterceptArgs(t, []string{"api-0", "--", "curl", "-s", "http://svc"})
		require.NoError(t, err)
		assert.Equal(t, "api-0", pod)
		assert.Equal(t, []string{"curl", "-s", "http://svc"}, command)
	})

	t.Run("pod only, no command", func(t *testing.T) {
		t.Parallel()
		pod, command, err := runInterceptArgs(t, []string{"api-0"})
		require.NoError(t, err)
		assert.Equal(t, "api-0", pod)
		assert.Empty(t, command)
	})

	t.Run("empty pod before dash errors", func(t *testing.T) {
		t.Parallel()
		_, _, err := runInterceptArgs(t, []string{"--", "curl"})
		require.Error(t, err)
	})

	t.Run("extra positional args without dash error", func(t *testing.T) {
		t.Parallel()
		_, _, err := runInterceptArgs(t, []string{"api-0", "extra"})
		require.Error(t, err)
	})
}

func TestRunIntercept_DisabledConfig(t *testing.T) {
	t.Parallel()
	cmd := newInterceptCmd()

	cases := map[string]*config.File{
		"nil config":     nil,
		"nil block":      {},
		"block disabled": {Intercept: &config.InterceptConfig{Enabled: false, AgentImage: "x"}},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := runIntercept(cmd, []string{"api-0"}, cfg, interceptFlags{modes: []string{"egress"}})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "not configured")
		})
	}
}

func TestRunIntercept_ValidationErrors(t *testing.T) {
	t.Parallel()
	enabled := &config.File{Intercept: &config.InterceptConfig{Enabled: true, AgentImage: "registry.example/agent:1"}}

	t.Run("unknown mode", func(t *testing.T) {
		t.Parallel()
		cmd := newInterceptCmd()
		err := runIntercept(cmd, []string{"api-0"}, enabled, interceptFlags{namespace: "apps", modes: []string{"bogus"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown mode")
	})

	t.Run("no mode and no config default", func(t *testing.T) {
		t.Parallel()
		cmd := newInterceptCmd()
		err := runIntercept(cmd, []string{"api-0"}, enabled, interceptFlags{namespace: "apps"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "at least one mode")
	})

	t.Run("incoming requires target", func(t *testing.T) {
		t.Parallel()
		cmd := newInterceptCmd()
		err := runIntercept(cmd, []string{"api-0"}, enabled, interceptFlags{namespace: "apps", modes: []string{"incoming"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--target is required")
	})

	t.Run("missing namespace", func(t *testing.T) {
		t.Parallel()
		cmd := newInterceptCmd()
		err := runIntercept(cmd, []string{"api-0"}, enabled, interceptFlags{modes: []string{"egress"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--namespace is required")
	})

	t.Run("missing agent image", func(t *testing.T) {
		t.Parallel()
		cmd := newInterceptCmd()
		cfg := &config.File{Intercept: &config.InterceptConfig{Enabled: true}}
		err := runIntercept(cmd, []string{"api-0"}, cfg, interceptFlags{namespace: "apps", modes: []string{"egress"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "agent image")
	})
}

func TestRunIntercept_ConfigDefaultMode(t *testing.T) {
	t.Parallel()
	// A config default_mode of incoming (with no flag) must still enforce the
	// --target rule: the default is resolved before validation.
	cmd := newInterceptCmd()
	cfg := &config.File{Intercept: &config.InterceptConfig{
		Enabled:     true,
		AgentImage:  "registry.example/agent:1",
		DefaultMode: []string{"incoming"},
	}}
	err := runIntercept(cmd, []string{"api-0"}, cfg, interceptFlags{namespace: "apps"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--target is required")
}

// TestRunIntercept_BrokeredDispatchPrecedesLocalValidation guards the ordering
// fix: the admin-url/fetchInterceptConfig brokered dispatch must run BEFORE
// the direct path's own mode/target validation. The local config's
// default_mode is "incoming" (which would require --target), but the
// server's default_mode is "egress" (no --target needed) — before the fix,
// the direct path's validation ran first using the LOCAL default and
// hard-errored on the missing --target before brokered dispatch was ever
// reached, even though a brokered session would validate against the
// server's own (non-incoming) default instead.
func TestRunIntercept_BrokeredDispatchPrecedesLocalValidation(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/intercept/config":
			_ = json.NewEncoder(w).Encode(map[string]any{"enabled": true, "default_mode": []string{"egress"}})
		case "/api/v1/kube/oidc-config":
			// Empty issuer/client_id: browserAuthCodeFlow fails fast here,
			// before any real OIDC discovery or loopback listener, so the test
			// stays fully local and deterministic.
			_ = json.NewEncoder(w).Encode(map[string]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cmd := newInterceptCmd()
	// runIntercept's brokered dispatch reads cmd.Context(); an un-Executed
	// cobra.Command has a nil ctx (only ExecuteContext/SetContext populate
	// it), which would make fetchInterceptConfig's http.NewRequestWithContext
	// fail before ever reaching the test server. Production commands always
	// go through cobra's Execute, which sets this.
	cmd.SetContext(context.Background())
	cfg := &config.File{Intercept: &config.InterceptConfig{
		Enabled:     true,
		AgentImage:  "registry.example/agent:1",
		DefaultMode: []string{"incoming"},
	}}
	err := runIntercept(cmd, []string{"api-0"}, cfg, interceptFlags{namespace: "apps", adminURL: srv.URL})
	require.Error(t, err)
	// Reached the brokered path's oidc step (validated against the server's
	// "egress" default, which needs no --target) instead of hard-erroring on
	// the local "incoming" default.
	assert.Contains(t, err.Error(), "oidc login")
	assert.NotContains(t, err.Error(), "--target is required")
}

func TestInterceptRestConfig_UnknownClusterErrors(t *testing.T) {
	// A --cluster name not present in k8s_proxy.clusters must error, not silently
	// fall back to the current kubeconfig context (gate/audit integrity).
	cfg := &config.File{K8sProxy: &config.K8sProxyConfig{Clusters: []config.K8sProxyCluster{{Name: "prod"}}}}
	_, err := interceptRestConfig(cfg, "staging")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not defined in k8s_proxy.clusters")

	// Nil k8s_proxy with a named cluster also errors.
	_, err = interceptRestConfig(&config.File{}, "prod")
	require.Error(t, err)
}
