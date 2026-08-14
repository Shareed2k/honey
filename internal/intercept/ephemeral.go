package intercept

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// capNetAdmin is the Linux capability the interception agent needs to program
// the target's network namespace (redirect rules, tunnel devices).
const capNetAdmin corev1.Capability = "NET_ADMIN"

// agentBypassGID is the group id the agent runs under when elevated so that its
// own egress bypasses the redirect it installs. It matches the agent's own
// default owner-bypass group.
const agentBypassGID int64 = 65533

// ephemeralPollInterval is how often waitEphemeralRunning re-reads the pod
// while waiting for the interception container to reach the running state.
const ephemeralPollInterval = 200 * time.Millisecond

// ephemeralContainer builds the EphemeralContainer spec for the interception
// agent, matching the data-plane agent's own recommended non-root security
// context: it runs image with args, keeps ONLY the NET_ADMIN capability (drops
// all others), never runs as root, and runs under the agent's bypass group so
// the agent's own egress skips the redirect it installs. It targets
// targetContainer to share that container's namespaces. Running non-root is why
// the token is delivered under /tmp (agentRunDir), which that user can write —
// unlike root-owned /var/run.
func ephemeralContainer(name, image, targetContainer string, args []string) corev1.EphemeralContainer {
	runAsGroup := agentBypassGID
	runAsNonRoot := true
	allowPrivilegeEscalation := false
	return corev1.EphemeralContainer{
		EphemeralContainerCommon: corev1.EphemeralContainerCommon{
			Name:  name,
			Image: image,
			Args:  args,
			SecurityContext: &corev1.SecurityContext{
				RunAsGroup:               &runAsGroup,
				RunAsNonRoot:             &runAsNonRoot,
				AllowPrivilegeEscalation: &allowPrivilegeEscalation,
				Capabilities: &corev1.Capabilities{
					Drop: []corev1.Capability{"ALL"},
					Add:  []corev1.Capability{capNetAdmin},
				},
			},
		},
		TargetContainerName: targetContainer,
	}
}

// elevateEphemeralPrivilege rewrites ec's security context to run privileged as
// root with the agent's bypass group. It is used only by the end-to-end test on
// a nested k3s where NET_ADMIN alone is not reliably propagated; production
// leaves the NET_ADMIN-only context ephemeralContainer builds.
func elevateEphemeralPrivilege(ec *corev1.EphemeralContainer) {
	privileged := true
	runAsUser := int64(0)
	runAsGroup := agentBypassGID
	ec.SecurityContext = &corev1.SecurityContext{
		Privileged: &privileged,
		RunAsUser:  &runAsUser,
		RunAsGroup: &runAsGroup,
		Capabilities: &corev1.Capabilities{
			Add: []corev1.Capability{capNetAdmin},
		},
	}
}

// applyEphemeral adds ec to the pod by reading the current pod, appending ec to
// its ephemeral containers, and updating the ephemeralcontainers subresource.
// Existing ephemeral containers are preserved.
func applyEphemeral(ctx context.Context, client kubernetes.Interface, ns, pod string, ec corev1.EphemeralContainer) error {
	p, err := client.CoreV1().Pods(ns).Get(ctx, pod, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("intercept: get pod %q: %w", pod, err)
	}
	p.Spec.EphemeralContainers = append(p.Spec.EphemeralContainers, ec)
	if _, err := client.CoreV1().Pods(ns).UpdateEphemeralContainers(ctx, pod, p, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("intercept: update ephemeral containers on pod %q: %w", pod, err)
	}
	return nil
}

// waitEphemeralRunning polls the pod until the ephemeral container named name
// reports a Running state, returning an error (wrapping the deadline cause) if
// timeout elapses first.
func waitEphemeralRunning(ctx context.Context, client kubernetes.Interface, ns, pod, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(ephemeralPollInterval)
	defer ticker.Stop()

	for {
		p, err := client.CoreV1().Pods(ns).Get(ctx, pod, metav1.GetOptions{})
		if err == nil && ephemeralContainerRunning(p, name) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("intercept: waiting for ephemeral container %q on pod %q: %w", name, pod, ctx.Err())
		case <-ticker.C:
		}
	}
}

// ephemeralContainerRunning reports whether pod's status shows the named
// ephemeral container in the Running state.
func ephemeralContainerRunning(pod *corev1.Pod, name string) bool {
	for i := range pod.Status.EphemeralContainerStatuses {
		s := &pod.Status.EphemeralContainerStatuses[i]
		if s.Name == name && s.State.Running != nil {
			return true
		}
	}
	return false
}
