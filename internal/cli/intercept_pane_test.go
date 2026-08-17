package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
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
	req := webserver.InterceptPaneRequest{
		Record: hosts.Record{Provider: "k8s", Meta: map[string]string{
			"kind": "pod", "namespace": "argocd", "pod_name": "api-0", "kube_context": "stg2",
		}},
		Modes: []string{"egress"}, Command: []string{"/bin/sh"}, Cols: 100, Rows: 30,
	}
	raw, err := json.Marshal(req)
	require.NoError(t, err)
	enc := base64.StdEncoding.EncodeToString(raw)

	decoded, err := decodeInterceptPaneRequest(enc)
	require.NoError(t, err)
	require.Equal(t, "api-0", decoded.Record.Meta["pod_name"])
	require.Equal(t, []string{"egress"}, decoded.Modes)
	require.Equal(t, 100, decoded.Cols)

	opts, err := intercept.OptionsFromPodRecord(decoded.Record, decoded.Modes, decoded.UDP, decoded.Command, "img:1")
	require.NoError(t, err)
	require.True(t, opts.Modes.Egress)
	require.Equal(t, "argocd", opts.Namespace)
	require.Equal(t, "api-0", opts.Pod)
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
