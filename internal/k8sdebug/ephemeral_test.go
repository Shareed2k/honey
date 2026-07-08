package k8sdebug

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestEnsureEphemeralContainer_Exists(t *testing.T) {
	clientset := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			EphemeralContainers: []corev1.EphemeralContainer{
				{
					EphemeralContainerCommon: corev1.EphemeralContainerCommon{
						Name: "honey-debug-123",
					},
				},
			},
		},
		Status: corev1.PodStatus{
			EphemeralContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "honey-debug-123",
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{},
					},
				},
			},
		},
	})

	ctx := context.Background()
	name, err := EnsureEphemeralContainer(ctx, clientset, "default", "test-pod", "")
	assert.NoError(t, err)
	assert.Equal(t, "honey-debug-123", name)
}

func TestEnsureEphemeralContainer_CreatesNew(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "main-container"},
			},
		},
	}
	clientset := fake.NewSimpleClientset(pod)

	// Create a fake watch that simulates the container becoming ready
	watcher := watch.NewFake()
	clientset.PrependWatchReactor("pods", ktesting.DefaultWatchReactor(watcher, nil))

	// In the background, after a short delay, push an event showing the container is running
	go func() {
		time.Sleep(50 * time.Millisecond)
		// We have to guess the name or just check what the client sent
		actions := clientset.Actions()
		for _, a := range actions {
			if a.GetVerb() == "update" && a.GetSubresource() == "ephemeralcontainers" {
				updateAction := a.(ktesting.UpdateAction)
				updatedPod := updateAction.GetObject().(*corev1.Pod)
				if len(updatedPod.Spec.EphemeralContainers) > 0 {
					ecName := updatedPod.Spec.EphemeralContainers[0].Name

					runningPod := updatedPod.DeepCopy()
					runningPod.Status.EphemeralContainerStatuses = []corev1.ContainerStatus{
						{
							Name: ecName,
							State: corev1.ContainerState{
								Running: &corev1.ContainerStateRunning{},
							},
						},
					}
					watcher.Add(runningPod)
					break
				}
			}
		}
	}()

	ctx := context.Background()
	name, err := EnsureEphemeralContainer(ctx, clientset, "default", "test-pod", "busybox")
	require.NoError(t, err)
	assert.Contains(t, name, "honey-debug-")

	// Verify it was added
	updated, err := clientset.CoreV1().Pods("default").Get(ctx, "test-pod", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Len(t, updated.Spec.EphemeralContainers, 1)
	assert.Equal(t, name, updated.Spec.EphemeralContainers[0].Name)
	assert.Equal(t, "busybox", updated.Spec.EphemeralContainers[0].Image)
}

func TestEnsureEphemeralContainer_PodNotFound(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	ctx := context.Background()
	_, err := EnsureEphemeralContainer(ctx, clientset, "default", "missing-pod", "")
	assert.ErrorContains(t, err, "get pod: pods \"missing-pod\" not found")
}

func TestWaitForEphemeralContainer_Timeout(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}
	clientset := fake.NewSimpleClientset(pod)

	watcher := watch.NewFake()
	clientset.PrependWatchReactor("pods", ktesting.DefaultWatchReactor(watcher, nil))

	// very short context
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := waitForEphemeralContainer(ctx, clientset, "default", "test-pod", "honey-debug-123")
	assert.ErrorContains(t, err, "waiting for ephemeral container")
}

func TestGenerateID(t *testing.T) {
	id1 := generateID()
	id2 := generateID()
	assert.Len(t, id1, 8)
	assert.Len(t, id2, 8)
	assert.NotEqual(t, id1, id2)
}
