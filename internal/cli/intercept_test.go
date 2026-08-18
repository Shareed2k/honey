package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
		wantEnv      bool
		wantErr      bool
	}{
		{name: "egress", in: []string{"egress"}, wantEgress: true},
		{name: "incoming", in: []string{"incoming"}, wantIncoming: true},
		{name: "files", in: []string{"files"}, wantFiles: true},
		{name: "env", in: []string{"env"}, wantEnv: true},
		{name: "egress and env", in: []string{"egress", "env"}, wantEgress: true, wantEnv: true},
		{name: "all four", in: []string{"egress", "incoming", "files", "env"}, wantEgress: true, wantIncoming: true, wantFiles: true, wantEnv: true},
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
			assert.Equal(t, tc.wantEnv, modes.Env)
		})
	}
}

func TestInterceptCmd_FlagParsing(t *testing.T) {
	t.Parallel()
	cmd := newInterceptCmd()
	require.NoError(t, cmd.ParseFlags([]string{
		"-n", "apps",
		"--container", "app",
		"--mode", "egress,env",
		"--env-include", "FOO,BAR",
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
	assert.Equal(t, []string{"egress", "env"}, modes)

	envInclude, err := cmd.Flags().GetStringSlice("env-include")
	require.NoError(t, err)
	assert.Equal(t, []string{"FOO", "BAR"}, envInclude)

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
// exactly as it is in production, then returns interceptArgs' result. Its Args
// validator mirrors newInterceptCmd's own (permissive: 0 or more positional
// arguments), since interceptArgs — not cobra — enforces the pod-count rule.
func runInterceptArgs(t *testing.T, argv []string) (pod string, targetless bool, command []string, err error) {
	t.Helper()
	cmd := &cobra.Command{
		Use:  "intercept",
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			pod, targetless, command, err = interceptArgs(c, args)
			return nil
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.SetArgs(argv)
	require.NoError(t, cmd.Execute())
	return pod, targetless, command, err
}

func TestInterceptArgs_CommandPassthrough(t *testing.T) {
	t.Parallel()

	t.Run("command after dash", func(t *testing.T) {
		t.Parallel()
		pod, targetless, command, err := runInterceptArgs(t, []string{"api-0", "--", "curl", "-s", "http://svc"})
		require.NoError(t, err)
		assert.Equal(t, "api-0", pod)
		assert.False(t, targetless)
		assert.Equal(t, []string{"curl", "-s", "http://svc"}, command)
	})

	t.Run("pod only, no command", func(t *testing.T) {
		t.Parallel()
		pod, targetless, command, err := runInterceptArgs(t, []string{"api-0"})
		require.NoError(t, err)
		assert.Equal(t, "api-0", pod)
		assert.False(t, targetless)
		assert.Empty(t, command)
	})

	t.Run("no positional and no command is targetless", func(t *testing.T) {
		t.Parallel()
		pod, targetless, command, err := runInterceptArgs(t, nil)
		require.NoError(t, err)
		assert.Empty(t, pod)
		assert.True(t, targetless)
		assert.Empty(t, command)
	})

	t.Run("no positional before dash is targetless", func(t *testing.T) {
		t.Parallel()
		pod, targetless, command, err := runInterceptArgs(t, []string{"--", "curl", "x"})
		require.NoError(t, err)
		assert.Empty(t, pod)
		assert.True(t, targetless)
		assert.Equal(t, []string{"curl", "x"}, command)
	})

	t.Run("literal empty-string positional errors", func(t *testing.T) {
		t.Parallel()
		_, _, _, err := runInterceptArgs(t, []string{"", "--", "curl"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "target pod name is empty")
	})

	t.Run("extra positional args without dash error", func(t *testing.T) {
		t.Parallel()
		_, _, _, err := runInterceptArgs(t, []string{"api-0", "extra"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expects at most one target pod")
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

	t.Run("missing agent image", func(t *testing.T) {
		t.Parallel()
		cmd := newInterceptCmd()
		cfg := &config.File{Intercept: &config.InterceptConfig{Enabled: true}}
		err := runIntercept(cmd, []string{"api-0"}, cfg, interceptFlags{namespace: "apps", modes: []string{"egress"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "agent image")
	})

	t.Run("env-include and env-exclude are mutually exclusive", func(t *testing.T) {
		t.Parallel()
		cmd := newInterceptCmd()
		err := runIntercept(cmd, []string{"api-0"}, enabled, interceptFlags{
			namespace:  "apps",
			modes:      []string{"env"},
			envInclude: []string{"FOO"},
			envExclude: []string{"BAR"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mutually exclusive")
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

// TestRunIntercept_Targetless covers the no-pod invocation: zero positional
// args (interceptArgs reports targetless=true), which must reject
// incoming/files modes and otherwise proceed past the targetless/mode
// validation exactly like a targeted invocation.
func TestRunIntercept_Targetless(t *testing.T) {
	t.Parallel()
	enabled := &config.File{Intercept: &config.InterceptConfig{Enabled: true, AgentImage: "registry.example/agent:1"}}
	const targetlessMsg = "targetless mode supports only egress"

	t.Run("incoming mode rejected", func(t *testing.T) {
		t.Parallel()
		cmd := newInterceptCmd()
		err := runIntercept(cmd, nil, enabled, interceptFlags{namespace: "apps", modes: []string{"incoming"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), targetlessMsg)
	})

	t.Run("files mode rejected", func(t *testing.T) {
		t.Parallel()
		cmd := newInterceptCmd()
		err := runIntercept(cmd, nil, enabled, interceptFlags{namespace: "apps", modes: []string{"files"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), targetlessMsg)
	})

	t.Run("env mode rejected", func(t *testing.T) {
		t.Parallel()
		cmd := newInterceptCmd()
		err := runIntercept(cmd, nil, enabled, interceptFlags{namespace: "apps", modes: []string{"env"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), targetlessMsg)
	})

	t.Run("configured default_mode incoming is still rejected", func(t *testing.T) {
		t.Parallel()
		cmd := newInterceptCmd()
		cfg := &config.File{Intercept: &config.InterceptConfig{
			Enabled:     true,
			AgentImage:  "registry.example/agent:1",
			DefaultMode: []string{"incoming"},
		}}
		err := runIntercept(cmd, nil, cfg, interceptFlags{namespace: "apps"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), targetlessMsg)
	})

	t.Run("egress mode passes the targetless check", func(t *testing.T) {
		t.Parallel()
		cmd := newInterceptCmd()
		// A cluster name absent from k8s_proxy.clusters forces a fast,
		// deterministic failure past the targetless/mode validation (inside
		// interceptwire.RestConfigForCluster, before any real cluster or policy work), so this
		// assertion doesn't depend on the host's kubeconfig or network.
		err := runIntercept(cmd, nil, enabled, interceptFlags{namespace: "apps", cluster: "does-not-exist", modes: []string{"egress"}})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), targetlessMsg)
	})

	t.Run("no mode flag defaults to egress and passes the targetless check", func(t *testing.T) {
		t.Parallel()
		cmd := newInterceptCmd()
		err := runIntercept(cmd, nil, enabled, interceptFlags{namespace: "apps", cluster: "does-not-exist"})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), targetlessMsg)
		assert.NotContains(t, err.Error(), "at least one mode")
	})
}

// TestRunIntercept_TargetlessSkipsBrokeredDispatch guards item 3 of the
// targetless design: server-brokered deploy of a standalone (podless) agent
// is out of scope, so a targetless invocation must never dispatch to the
// brokered path even when an admin URL is configured — it always takes the
// direct path.
func TestRunIntercept_TargetlessSkipsBrokeredDispatch(t *testing.T) {
	t.Parallel()
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": true, "default_mode": []string{"egress"}})
	}))
	defer srv.Close()

	cmd := newInterceptCmd()
	cmd.SetContext(context.Background())
	cfg := &config.File{Intercept: &config.InterceptConfig{Enabled: true, AgentImage: "registry.example/agent:1"}}
	// A cluster name absent from k8s_proxy.clusters gives the direct path a
	// fast, deterministic failure, so a passing test proves brokered dispatch
	// was skipped rather than merely failing for an unrelated reason.
	err := runIntercept(cmd, nil, cfg, interceptFlags{namespace: "apps", cluster: "does-not-exist", modes: []string{"egress"}, adminURL: srv.URL})
	require.Error(t, err)
	assert.False(t, called, "brokered dispatch must be skipped for a targetless invocation")
	assert.Contains(t, err.Error(), "not defined in k8s_proxy.clusters")
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
	// --cluster is supplied so the brokered path clears its up-front cluster
	// check and reaches the oidc step; this test is about the incoming/--target
	// validation ordering, which is orthogonal to the cluster requirement.
	err := runIntercept(cmd, []string{"api-0"}, cfg, interceptFlags{namespace: "apps", cluster: "prod", adminURL: srv.URL})
	require.Error(t, err)
	// Reached the brokered path's oidc step (validated against the server's
	// "egress" default, which needs no --target) instead of hard-erroring on
	// the local "incoming" default.
	assert.Contains(t, err.Error(), "oidc login")
	assert.NotContains(t, err.Error(), "--target is required")
}

// writeNSKubeconfig writes a kubeconfig whose current context selects namespace
// "team-a" and whose "ctx-none" context selects no namespace.
func writeNSKubeconfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "kubeconfig")
	data := `
apiVersion: v1
kind: Config
clusters:
- name: c
  cluster:
    server: https://c.example.com
contexts:
- name: ctx-ns
  context:
    cluster: c
    namespace: team-a
- name: ctx-none
  context:
    cluster: c
current-context: ctx-ns
users: []
`
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
	return path
}

func TestInterceptNamespace(t *testing.T) {
	t.Run("explicit --namespace wins", func(t *testing.T) {
		ns, err := interceptNamespace(&config.File{}, "", "  payments  ")
		require.NoError(t, err)
		assert.Equal(t, "payments", ns)
	})

	t.Run("unknown --cluster errors", func(t *testing.T) {
		_, err := interceptNamespace(&config.File{}, "nope", "")
		require.Error(t, err)
		assert.ErrorContains(t, err, "not defined in k8s_proxy.clusters")
	})

	t.Run("named cluster falls back to its context namespace", func(t *testing.T) {
		kubeconfig := writeNSKubeconfig(t)
		cfg := &config.File{K8sProxy: &config.K8sProxyConfig{
			Clusters: []config.K8sProxyCluster{{Name: "stg", Kubeconfig: kubeconfig, Context: "ctx-ns"}},
		}}
		ns, err := interceptNamespace(cfg, "stg", "")
		require.NoError(t, err)
		assert.Equal(t, "team-a", ns)
	})

	t.Run("no cluster falls back to current-context namespace (like kubectl)", func(t *testing.T) {
		t.Setenv("KUBECONFIG", writeNSKubeconfig(t))
		ns, err := interceptNamespace(&config.File{}, "", "")
		require.NoError(t, err)
		assert.Equal(t, "team-a", ns)
	})
}
