//go:build !windows

package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/shareed2k/mogate/pkg/local"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/intercept"
	"github.com/shareed2k/honey/internal/webserver"
)

// TestInterceptPaneDecode round-trips the base64(JSON) payload and asserts the
// decoded record maps to intercept.Options through the shared pod-record mapper,
// exactly as the running pane does before it deploys anything.
func TestInterceptPaneDecode(t *testing.T) {
	t.Parallel()
	// Table-driven over EnvInclude-only and EnvExclude-only (never both — the
	// pane rejects that combination, see TestRunInterceptPane_RejectsEnvIncludeAndExclude):
	// each field must independently survive the base64(JSON) round-trip and
	// land on intercept.Options exactly like runInterceptPane sets it.
	cases := []struct {
		name       string
		envInclude []string
		envExclude []string
	}{
		{name: "env_include", envInclude: []string{"DATABASE_URL"}},
		{name: "env_exclude", envExclude: []string{"SECRET_KEY"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := webserver.InterceptPaneRequest{
				Record: hosts.Record{Provider: "k8s", Meta: map[string]string{
					"kind": "pod", "namespace": "argocd", "pod_name": "api-0", "kube_context": "stg2",
				}},
				Modes:      []string{"egress"},
				Command:    []string{"/bin/sh"},
				Container:  "  app  ", // the pane must trim, matching buildInterceptOptions
				EnvInclude: tc.envInclude,
				EnvExclude: tc.envExclude,
				Actor:      "alice",
				Cols:       100,
				Rows:       30,
			}
			raw, err := json.Marshal(req)
			require.NoError(t, err)
			enc := base64.StdEncoding.EncodeToString(raw)

			decoded, err := decodeInterceptPaneRequest(enc)
			require.NoError(t, err)
			require.Equal(t, "api-0", decoded.Record.Meta["pod_name"])
			require.Equal(t, []string{"egress"}, decoded.Modes)
			require.Equal(t, 100, decoded.Cols)
			// Container/EnvInclude/EnvExclude/Actor must survive the round-trip:
			// the mapper (OptionsFromPodRecord) omits them, so the pane sets
			// them itself (mirroring the web fallback's buildInterceptOptions),
			// and they'd otherwise be silently dropped.
			require.Equal(t, "  app  ", decoded.Container, "decode itself must not trim")
			require.Equal(t, tc.envInclude, decoded.EnvInclude)
			require.Equal(t, tc.envExclude, decoded.EnvExclude)
			require.Equal(t, "alice", decoded.Actor)

			opts, err := intercept.OptionsFromPodRecord(decoded.Record, decoded.Modes, decoded.UDP, decoded.Command, "img:1")
			require.NoError(t, err)
			// Mirrors the field-copy runInterceptPane performs, trim included.
			opts.Container = strings.TrimSpace(decoded.Container)
			opts.EnvInclude = decoded.EnvInclude
			opts.EnvExclude = decoded.EnvExclude
			opts.Actor = decoded.Actor
			require.True(t, opts.Modes.Egress)
			require.Equal(t, "argocd", opts.Namespace)
			require.Equal(t, "api-0", opts.Pod)
			require.Equal(t, "app", opts.Container, "runInterceptPane must trim Container")
			require.Equal(t, tc.envInclude, opts.EnvInclude)
			require.Equal(t, tc.envExclude, opts.EnvExclude)
			require.Equal(t, "alice", opts.Actor)
		})
	}
}

// TestRunInterceptPane_RejectsEnvIncludeAndExclude proves the pane enforces
// the same env_include/env_exclude mutual-exclusion buildInterceptOptions
// enforces on the fallback path — the shared mapper doesn't check it, so each
// caller must.
func TestRunInterceptPane_RejectsEnvIncludeAndExclude(t *testing.T) {
	t.Parallel()
	req := webserver.InterceptPaneRequest{
		Record:     hosts.Record{Provider: "k8s", Meta: map[string]string{"kind": "pod", "namespace": "argocd", "pod_name": "api-0"}},
		Modes:      []string{"env"},
		EnvInclude: []string{"DATABASE_URL"},
		EnvExclude: []string{"SECRET_KEY"},
	}
	raw, err := json.Marshal(req)
	require.NoError(t, err)
	enc := base64.StdEncoding.EncodeToString(raw)

	err = runInterceptPane(context.Background(), enc)
	require.ErrorContains(t, err, "mutually exclusive")
}

// TestInterceptPaneDecodeRejectsGarbage rejects a non-base64 argv value with a
// wrapped error instead of panicking the pane.
func TestInterceptPaneDecodeRejectsGarbage(t *testing.T) {
	t.Parallel()
	_, err := decodeInterceptPaneRequest("!!!not base64!!!")
	require.Error(t, err)
}

// TestInterceptPaneResizeExitsOnCancel verifies the SIGWINCH resize feeder seeds
// the initial size and its goroutine exits (closing the channel) when its
// context is cancelled, so the pane leaks no goroutine on teardown.
func TestInterceptPaneResizeExitsOnCancel(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	ctx, cancel := context.WithCancel(context.Background())
	ch := paneResizeChan(ctx, int(os.Stdin.Fd()), 100, 30)

	require.Equal(t, local.Winsize{Cols: 100, Rows: 30}, <-ch)

	cancel()
	// The goroutine closes ch on ctx cancel; a receive that reports the channel
	// closed is the deterministic signal that it exited.
	_, ok := <-ch
	require.False(t, ok, "resize feeder must close its channel on ctx cancel")
}
