package intercept

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// AgentContainerName is the single container name in a standalone (targetless)
// agent Pod. It is exported so the CLI's execer (internal/cli/intercept.go)
// can target it directly instead of resolving the last-added ephemeral
// container, which targetless mode has none of.
const AgentContainerName = "mogate-agent"

// agentPodPrefix names the standalone targetless agent Pod; a random suffix
// is appended so concurrent targetless sessions never collide. Mirrors
// agentContainerPrefix (session.go), which does the same for the targeted
// path's ephemeral container name.
const agentPodPrefix = "mogate"

// labelManagedBy and labelSession are the labels set on a standalone agent
// Pod: the first identifies honey as the owner, the second names the session
// (the pod's own name) so operators can find a Pod's session at a glance.
const (
	labelManagedBy      = "app.kubernetes.io/managed-by"
	labelManagedByValue = "honey-intercept"
	labelSession        = "intercept.honey/session"
)

// NewAgentPodName returns a fresh, unique DNS-1123-compliant name for a
// standalone targetless agent Pod, of the form "<prefix>-<random hex>". It
// draws the same number of random bytes as agentContainerName (session.go)
// does for the targeted path's ephemeral container name, so both share the
// same collision-resistance and length characteristics.
func NewAgentPodName() (string, error) {
	b := make([]byte, agentNameRandomBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("intercept: generate agent pod name: %w", err)
	}
	return agentPodPrefix + "-" + hex.EncodeToString(b), nil
}

// standaloneAgentPod builds the standalone (targetless) agent Pod spec: one
// container named AgentContainerName running image with args, unprivileged —
// non-root, no privilege escalation, and every Linux capability dropped. This
// is deliberately weaker than the targeted path's ephemeral container (which
// runs as root with NET_ADMIN to program the target's network namespace):
// targetless has no target namespace to program, so egress and DNS are
// handled entirely in userspace and the agent needs no elevated privilege at
// all. The Pod never restarts on its own (RestartPolicyNever): honey owns its
// full lifecycle and deletes it on teardown.
func standaloneAgentPod(name, ns, image string, args []string) *corev1.Pod {
	nonRoot := true
	noPrivilegeEscalation := false
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels: map[string]string{
				labelManagedBy: labelManagedByValue,
				labelSession:   name,
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:  AgentContainerName,
					Image: image,
					Args:  args,
					SecurityContext: &corev1.SecurityContext{
						RunAsNonRoot:             &nonRoot,
						AllowPrivilegeEscalation: &noPrivilegeEscalation,
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{"ALL"},
						},
					},
				},
			},
		},
	}
}

// standaloneAgentArgs builds the standalone agent's container arguments: the
// subcommand, --no-redirect (egress-only: it must not install any network
// redirects, since there is no target pod namespace to redirect), the in-agent
// token file path, and the UDP toggle. Reuses agentSubcommand (session.go) and
// agentRunDir/tokenFileName (token.go) so the path stays consistent with
// deliverToken, which writes the token to exactly this location.
func standaloneAgentArgs(udp bool) []string {
	return []string{
		agentSubcommand,
		"--no-redirect",
		"--token-file", agentRunDir + "/" + tokenFileName,
		fmt.Sprintf("--udp=%t", udp),
	}
}

// createAgentPod creates the standalone agent pod in the cluster.
func createAgentPod(ctx context.Context, client kubernetes.Interface, ns string, pod *corev1.Pod) error {
	if _, err := client.CoreV1().Pods(ns).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("intercept: create agent pod %q: %w", pod.Name, err)
	}
	return nil
}

// waitPodRunning polls the named pod until it reports phase Running and its
// AgentContainerName container is Ready, returning an error (wrapping the
// deadline cause) if timeout elapses first. Mirrors waitEphemeralRunning
// (ephemeral.go), which does the same for the targeted path's ephemeral
// container.
func waitPodRunning(ctx context.Context, client kubernetes.Interface, ns, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(ephemeralPollInterval)
	defer ticker.Stop()

	for {
		p, err := client.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
		if err == nil && podRunningAndReady(p) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("intercept: waiting for agent pod %q: %w", name, ctx.Err())
		case <-ticker.C:
		}
	}
}

// podRunningAndReady reports whether pod's status shows phase Running with
// its AgentContainerName container Ready.
func podRunningAndReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for i := range pod.Status.ContainerStatuses {
		cs := &pod.Status.ContainerStatuses[i]
		if cs.Name == AgentContainerName {
			return cs.Ready
		}
	}
	return false
}

// deleteAgentPod deletes the standalone agent pod immediately (zero grace
// period): honey owns the Pod's full lifecycle and the agent's own graceful
// shutdown is not load-bearing here (unlike the targeted path's ephemeral
// container, which cannot be deleted at all and so is instead asked to
// terminate in place). A not-found error is tolerated so a caller can call
// this best-effort on teardown even if the Pod was never fully created.
func deleteAgentPod(ctx context.Context, client kubernetes.Interface, ns, name string) error {
	gracePeriod := int64(0)
	err := client.CoreV1().Pods(ns).Delete(ctx, name, metav1.DeleteOptions{GracePeriodSeconds: &gracePeriod})
	if err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("intercept: delete agent pod %q: %w", name, err)
	}
	return nil
}
