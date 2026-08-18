package interceptwire

import (
	"context"
	"fmt"
	"io"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/shareed2k/honey/internal/provider/k8sprovider"
)

// PodExecer delivers the session token into the interception agent by
// executing a command in the pod's agent container. It satisfies
// intercept.PodExecer.
type PodExecer struct {
	Cfg       *rest.Config
	Clientset kubernetes.Interface
	Namespace string
	Pod       string
	// Container names the agent container directly, skipping the
	// ephemeral-container lookup. Set by the targetless path, where the
	// standalone pod's single container has a known, fixed name
	// (intercept.AgentContainerName). Left empty for the targeted path, which
	// falls back to resolving the most recently added ephemeral container.
	Container string
}

// ExecInPod runs cmd in the pod's agent container, wiring the provided
// streams. When Container is set (the targetless path), it is used directly.
// Otherwise the agent container is resolved at exec time because the session
// generates its ephemeral container's name at run time and delivers the token
// without threading that name through; the most recently added ephemeral
// container is the session's own agent.
func (e *PodExecer) ExecInPod(ctx context.Context, cmd []string, stdin io.Reader, stdout, stderr io.Writer) error {
	container := e.Container
	if container == "" {
		var err error
		container, err = e.agentContainer(ctx)
		if err != nil {
			return err
		}
	}
	return ExecInPodContainer(ctx, e.Cfg, e.Namespace, e.Pod, container, cmd, stdin, stdout, stderr)
}

// agentContainer returns the name of the pod's agent container: the most
// recently added ephemeral container, which is this session's agent.
func (e *PodExecer) agentContainer(ctx context.Context) (string, error) {
	p, err := e.Clientset.CoreV1().Pods(e.Namespace).Get(ctx, e.Pod, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("intercept: get pod %q: %w", e.Pod, err)
	}
	ecs := p.Spec.EphemeralContainers
	if len(ecs) == 0 {
		return "", fmt.Errorf("intercept: no agent container on pod %q", e.Pod)
	}
	return ecs[len(ecs)-1].Name, nil
}

// ExecInPodContainer runs cmd in a specific container of a pod over an SPDY
// exec stream, wiring stdin/stdout/stderr. It builds its own Kubernetes client
// from cfg on every call: exec is infrequent (one call per token delivery or
// teardown signal), so a fresh client per call costs nothing worth caching.
//
// It is the shared exec used by both the CLI's direct-path execer (PodExecer,
// which resolves its container from the pod's most recently added ephemeral
// container) and the server broker's execer (cli's brokerPodExecer, which
// already knows its container name from the agent it just deployed).
func ExecInPodContainer(ctx context.Context, cfg *rest.Config, ns, pod, container string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) error {
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
