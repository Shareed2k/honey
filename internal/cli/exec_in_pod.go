package cli

import (
	"context"
	"fmt"
	"io"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/shareed2k/honey/internal/provider/k8sprovider"
)

// execInPodContainer runs cmd in a specific container of a pod over an SPDY
// exec stream, wiring stdin/stdout/stderr. It builds its own Kubernetes client
// from cfg on every call: exec is infrequent (one call per token delivery or
// teardown signal), so a fresh client per call costs nothing worth caching.
//
// It is the shared exec used by both the CLI's direct-path execer
// (interceptPodExecer, which resolves its container from the pod's most
// recently added ephemeral container) and the server broker's execer
// (brokerPodExecer, which already knows its container name from the agent it
// just deployed).
func execInPodContainer(ctx context.Context, cfg *rest.Config, ns, pod, container string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) error {
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("intercept: build kubernetes client: %w", err)
	}
	client := &k8sprovider.K8sNativeClient{
		Config:    cfg,
		Clientset: clientset,
		Namespace: ns,
		PodName:   pod,
		Container: container,
	}
	return client.ExecInPod(ctx, cmd, stdin, stdout, stderr, false, nil)
}
