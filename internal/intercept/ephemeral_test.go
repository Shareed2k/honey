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

	ec := ephemeralContainer("honey-x", "registry.example/agent:1", "app", []string{"--mode", "egress"}, false)

	assert.Equal(t, "honey-x", ec.Name)
	assert.Equal(t, "registry.example/agent:1", ec.Image)
	assert.Equal(t, "app", ec.TargetContainerName)
	assert.Equal(t, []string{"--mode", "egress"}, ec.Args)

	// The agent installs nftables via netlink, which needs an EFFECTIVE
	// CAP_NET_ADMIN — only root gets that from capabilities.add (a non-root user
	// would have it merely permitted, and the stock image ships no file caps),
	// so the production context runs as uid 0 with only NET_ADMIN, under the
	// bypass GID so the agent's own egress skips its redirect. A regression to
	// non-root fails the nftables install with "operation not permitted" on real
	// clusters (the e2e always elevates, so it cannot catch this).
	sc := ec.SecurityContext
	require.NotNil(t, sc)
	require.NotNil(t, sc.Capabilities)
	// Without env mode the context is byte-for-byte the network-only one: only
	// NET_ADMIN is added — the /proc-read caps must NOT leak onto every intercept
	// (least privilege).
	assert.Equal(t, []corev1.Capability{"NET_ADMIN"}, sc.Capabilities.Add)
	assert.NotContains(t, sc.Capabilities.Add, capSysPtrace)
	assert.NotContains(t, sc.Capabilities.Add, capDacReadSearch)
	assert.Equal(t, []corev1.Capability{"ALL"}, sc.Capabilities.Drop)
	require.NotNil(t, sc.RunAsUser)
	assert.Equal(t, int64(0), *sc.RunAsUser, "agent needs root for an effective NET_ADMIN (nftables via netlink)")
	require.NotNil(t, sc.RunAsGroup)
	assert.Equal(t, agentBypassGID, *sc.RunAsGroup, "agent must run under the bypass GID for its own-egress bypass")
	assert.Nil(t, sc.RunAsNonRoot, "must not assert non-root while running as uid 0")
	// The token dir is under /tmp, written by honey's exec delivery.
	assert.Equal(t, "/tmp/mogate", agentRunDir)
}

// TestEphemeralContainer_envCaps asserts that env mode (needsProcRead=true)
// adds CAP_SYS_PTRACE and CAP_DAC_READ_SEARCH so the agent can read the target
// container's /proc/1/environ, on top of the always-present NET_ADMIN, while
// still dropping ALL others. These caps are the least privilege the env overlay
// needs and are added only for env mode.
func TestEphemeralContainer_envCaps(t *testing.T) {
	t.Parallel()

	ec := ephemeralContainer("honey-x", "registry.example/agent:1", "app", []string{"--mode", "env"}, true)

	sc := ec.SecurityContext
	require.NotNil(t, sc)
	require.NotNil(t, sc.Capabilities)
	assert.Equal(t, []corev1.Capability{capNetAdmin, capSysPtrace, capDacReadSearch}, sc.Capabilities.Add)
	assert.Contains(t, sc.Capabilities.Add, capSysPtrace)
	assert.Contains(t, sc.Capabilities.Add, capDacReadSearch)
	// ALL is still dropped and the run-as identity is unchanged from the
	// network-only path.
	assert.Equal(t, []corev1.Capability{"ALL"}, sc.Capabilities.Drop)
	require.NotNil(t, sc.RunAsUser)
	assert.Equal(t, int64(0), *sc.RunAsUser)
	require.NotNil(t, sc.RunAsGroup)
	assert.Equal(t, agentBypassGID, *sc.RunAsGroup)
}

func TestApplyEphemeral(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset(newSeededPod())
	ec := ephemeralContainer("honey-x", "registry.example/agent:1", "app", []string{"--mode", "egress"}, false)

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
	ec := ephemeralContainer("honey-x", "img", "app", nil, false)

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
