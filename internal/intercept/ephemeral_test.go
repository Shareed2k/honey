package intercept

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func newSeededPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "target", Namespace: "apps"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app"}},
		},
	}
}

func TestEphemeralContainer(t *testing.T) {
	t.Parallel()

	ec := ephemeralContainer("honey-x", "registry.example/agent:1", "app", []string{"--mode", "egress"})

	assert.Equal(t, "honey-x", ec.Name)
	assert.Equal(t, "registry.example/agent:1", ec.Image)
	assert.Equal(t, "app", ec.TargetContainerName)
	assert.Equal(t, []string{"--mode", "egress"}, ec.Args)

	// The production (non-elevated) context must be non-root, keep only
	// NET_ADMIN, and run under the agent's bypass group so the agent's own
	// egress skips its redirect. Regressions here silently break real clusters
	// (the e2e always elevates to root, so it cannot catch this).
	sc := ec.SecurityContext
	require.NotNil(t, sc)
	require.NotNil(t, sc.Capabilities)
	assert.Equal(t, []corev1.Capability{"NET_ADMIN"}, sc.Capabilities.Add)
	assert.Equal(t, []corev1.Capability{"ALL"}, sc.Capabilities.Drop)
	require.NotNil(t, sc.RunAsNonRoot)
	assert.True(t, *sc.RunAsNonRoot, "agent must not run as root in production")
	require.NotNil(t, sc.RunAsGroup)
	assert.Equal(t, agentBypassGID, *sc.RunAsGroup, "agent must run under the bypass GID for its own-egress bypass")
	require.NotNil(t, sc.AllowPrivilegeEscalation)
	assert.False(t, *sc.AllowPrivilegeEscalation)
	// The token dir must be writable by that non-root user (i.e. under /tmp).
	assert.Equal(t, "/tmp/mogate", agentRunDir)
}

func TestApplyEphemeral(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset(newSeededPod())
	ec := ephemeralContainer("honey-x", "registry.example/agent:1", "app", []string{"--mode", "egress"})

	require.NoError(t, applyEphemeral(context.Background(), client, "apps", "target", ec))

	got, err := client.CoreV1().Pods("apps").Get(context.Background(), "target", metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, got.Spec.EphemeralContainers, 1)
	assert.Equal(t, "honey-x", got.Spec.EphemeralContainers[0].Name)
	assert.Equal(t, "registry.example/agent:1", got.Spec.EphemeralContainers[0].Image)
	assert.Equal(t,
		[]corev1.Capability{"NET_ADMIN"},
		got.Spec.EphemeralContainers[0].SecurityContext.Capabilities.Add,
	)
}

func TestApplyEphemeral_podNotFound(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset()
	ec := ephemeralContainer("honey-x", "img", "app", nil)

	err := applyEphemeral(context.Background(), client, "apps", "missing", ec)
	require.Error(t, err)
	assert.ErrorContains(t, err, "get pod")
}

func TestWaitEphemeralRunning_alreadyRunning(t *testing.T) {
	t.Parallel()

	pod := newSeededPod()
	pod.Status.EphemeralContainerStatuses = []corev1.ContainerStatus{
		{Name: "honey-x", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
	}
	client := fake.NewSimpleClientset(pod)

	err := waitEphemeralRunning(context.Background(), client, "apps", "target", "honey-x", 2*time.Second)
	require.NoError(t, err)
}

func TestWaitEphemeralRunning_flipsToRunning(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset(newSeededPod())

	// Flip the ephemeral container to Running shortly after the wait begins.
	go func() {
		time.Sleep(20 * time.Millisecond)
		p, err := client.CoreV1().Pods("apps").Get(context.Background(), "target", metav1.GetOptions{})
		if err != nil {
			return
		}
		p.Status.EphemeralContainerStatuses = []corev1.ContainerStatus{
			{Name: "honey-x", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
		}
		_, _ = client.CoreV1().Pods("apps").UpdateStatus(context.Background(), p, metav1.UpdateOptions{})
	}()

	err := waitEphemeralRunning(context.Background(), client, "apps", "target", "honey-x", 3*time.Second)
	require.NoError(t, err)
}

func TestWaitEphemeralRunning_timeout(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset(newSeededPod())

	err := waitEphemeralRunning(context.Background(), client, "apps", "target", "honey-x", 30*time.Millisecond)
	require.Error(t, err)
	assert.ErrorContains(t, err, "waiting for ephemeral container")
}
