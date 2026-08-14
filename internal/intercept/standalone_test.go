package intercept

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// dns1123LabelRE mirrors Kubernetes' own DNS-1123 label validation: lowercase
// alphanumeric characters or '-', starting and ending with an alphanumeric.
var dns1123LabelRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

func TestNewAgentPodName(t *testing.T) {
	t.Parallel()

	a, err := NewAgentPodName()
	require.NoError(t, err)
	b, err := NewAgentPodName()
	require.NoError(t, err)

	assert.NotEqual(t, a, b, "each call must return a unique name")
	for _, name := range []string{a, b} {
		assert.LessOrEqual(t, len(name), 63, "must fit a DNS-1123 label")
		assert.Truef(t, dns1123LabelRE.MatchString(name), "name %q must be a valid DNS-1123 label", name)
		assert.Regexp(t, `^mogate-[0-9a-f]+$`, name)
	}
}

func TestStandaloneAgentPod(t *testing.T) {
	t.Parallel()

	pod := standaloneAgentPod("mogate-abc123", "apps", "registry.example/agent:1", standaloneAgentArgs(true))

	assert.Equal(t, "mogate-abc123", pod.Name)
	assert.Equal(t, "apps", pod.Namespace)
	assert.Equal(t, "honey-intercept", pod.Labels["app.kubernetes.io/managed-by"])
	assert.Equal(t, "mogate-abc123", pod.Labels["intercept.honey/session"])
	assert.Equal(t, corev1.RestartPolicyNever, pod.Spec.RestartPolicy)

	require.Len(t, pod.Spec.Containers, 1)
	c := pod.Spec.Containers[0]
	assert.Equal(t, AgentContainerName, c.Name)
	assert.Equal(t, "mogate-agent", AgentContainerName)
	assert.Equal(t, "registry.example/agent:1", c.Image)
	assert.Contains(t, c.Args, "--no-redirect")
	assert.Contains(t, c.Args, "--token-file")
	// The token path must land where deliverToken (token.go) writes it.
	idx := indexOfStr(c.Args, "--token-file")
	require.GreaterOrEqual(t, idx, 0)
	require.Less(t, idx+1, len(c.Args))
	assert.Equal(t, "/tmp/mogate/token", c.Args[idx+1])
	assert.Contains(t, c.Args, "--udp=true")

	// Unprivileged: no NET_ADMIN, not root, no privilege escalation, every
	// capability dropped. This is the whole point of targetless — egress and
	// DNS are userspace, so the standalone agent needs no elevated privilege
	// at all (unlike the targeted path's ephemeral container).
	sc := c.SecurityContext
	require.NotNil(t, sc)
	require.NotNil(t, sc.RunAsNonRoot)
	assert.True(t, *sc.RunAsNonRoot)
	require.NotNil(t, sc.AllowPrivilegeEscalation)
	assert.False(t, *sc.AllowPrivilegeEscalation)
	require.NotNil(t, sc.Capabilities)
	assert.Equal(t, []corev1.Capability{"ALL"}, sc.Capabilities.Drop)
	assert.Empty(t, sc.Capabilities.Add, "must not add NET_ADMIN or any other capability")
	assert.Nil(t, sc.Privileged)
}

// indexOfStr returns the index of want in ss, or -1.
func indexOfStr(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}
	return -1
}

func TestStandaloneAgentArgs(t *testing.T) {
	t.Parallel()

	udpOff := standaloneAgentArgs(false)
	assert.Equal(t, []string{"kube-agent", "--no-redirect", "--token-file", "/tmp/mogate/token", "--udp=false"}, udpOff)

	udpOn := standaloneAgentArgs(true)
	assert.Equal(t, []string{"kube-agent", "--no-redirect", "--token-file", "/tmp/mogate/token", "--udp=true"}, udpOn)
}

func TestCreateAgentPod(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset()
	pod := standaloneAgentPod("mogate-abc123", "apps", "registry.example/agent:1", standaloneAgentArgs(false))

	require.NoError(t, createAgentPod(context.Background(), client, "apps", pod))

	got, err := client.CoreV1().Pods("apps").Get(context.Background(), "mogate-abc123", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "mogate-abc123", got.Name)
}

func TestCreateAgentPod_alreadyExists(t *testing.T) {
	t.Parallel()

	pod := standaloneAgentPod("mogate-abc123", "apps", "img", nil)
	client := fake.NewSimpleClientset(pod)

	err := createAgentPod(context.Background(), client, "apps", pod)
	require.Error(t, err)
	assert.ErrorContains(t, err, "create agent pod")
}

func TestWaitPodRunning_alreadyRunning(t *testing.T) {
	t.Parallel()

	pod := standaloneAgentPod("mogate-abc123", "apps", "img", nil)
	pod.Status.Phase = corev1.PodRunning
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{
		{Name: AgentContainerName, Ready: true},
	}
	client := fake.NewSimpleClientset(pod)

	err := waitPodRunning(context.Background(), client, "apps", "mogate-abc123", 2*time.Second)
	require.NoError(t, err)
}

func TestWaitPodRunning_notReadyDoesNotCount(t *testing.T) {
	t.Parallel()

	pod := standaloneAgentPod("mogate-abc123", "apps", "img", nil)
	pod.Status.Phase = corev1.PodRunning
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{
		{Name: AgentContainerName, Ready: false},
	}
	client := fake.NewSimpleClientset(pod)

	err := waitPodRunning(context.Background(), client, "apps", "mogate-abc123", 30*time.Millisecond)
	require.Error(t, err)
	assert.ErrorContains(t, err, "waiting for agent pod")
}

func TestWaitPodRunning_flipsToRunning(t *testing.T) {
	t.Parallel()

	pod := standaloneAgentPod("mogate-abc123", "apps", "img", nil)
	client := fake.NewSimpleClientset(pod)

	go func() {
		time.Sleep(20 * time.Millisecond)
		p, err := client.CoreV1().Pods("apps").Get(context.Background(), "mogate-abc123", metav1.GetOptions{})
		if err != nil {
			return
		}
		p.Status.Phase = corev1.PodRunning
		p.Status.ContainerStatuses = []corev1.ContainerStatus{
			{Name: AgentContainerName, Ready: true},
		}
		_, _ = client.CoreV1().Pods("apps").UpdateStatus(context.Background(), p, metav1.UpdateOptions{})
	}()

	err := waitPodRunning(context.Background(), client, "apps", "mogate-abc123", 3*time.Second)
	require.NoError(t, err)
}

func TestWaitPodRunning_timeout(t *testing.T) {
	t.Parallel()

	pod := standaloneAgentPod("mogate-abc123", "apps", "img", nil)
	client := fake.NewSimpleClientset(pod)

	err := waitPodRunning(context.Background(), client, "apps", "mogate-abc123", 30*time.Millisecond)
	require.Error(t, err)
	assert.ErrorContains(t, err, "waiting for agent pod")
}

func TestDeleteAgentPod(t *testing.T) {
	t.Parallel()

	pod := standaloneAgentPod("mogate-abc123", "apps", "img", nil)
	client := fake.NewSimpleClientset(pod)

	require.NoError(t, deleteAgentPod(context.Background(), client, "apps", "mogate-abc123"))

	_, err := client.CoreV1().Pods("apps").Get(context.Background(), "mogate-abc123", metav1.GetOptions{})
	require.Error(t, err)
	assert.True(t, k8serrors.IsNotFound(err))
}

func TestDeleteAgentPod_notFoundIsTolerated(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset()
	require.NoError(t, deleteAgentPod(context.Background(), client, "apps", "does-not-exist"))
}
