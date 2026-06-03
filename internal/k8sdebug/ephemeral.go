// Package k8sdebug provides utilities for Kubernetes debug containers.
package k8sdebug

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func generateID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// rand.Read on crypto/rand should only fail if the system's random number generator fails.
		// Fallback to time if absolutely necessary or just panic.
		panic(fmt.Errorf("failed to generate random ID: %w", err))
	}
	return hex.EncodeToString(b)
}

// EnsureEphemeralContainer creates or waits for an ephemeral debug container in a pod.
func EnsureEphemeralContainer(ctx context.Context, clientset kubernetes.Interface, namespace, podName, image string) (string, error) {
	if image == "" {
		image = "alpine:3.23"
	}

	pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get pod: %w", err)
	}

	// Look for an existing honey-debug container
	for _, ec := range pod.Spec.EphemeralContainers {
		if strings.HasPrefix(ec.Name, "honey-debug") {
			return ec.Name, waitForEphemeralContainer(ctx, clientset, namespace, podName, ec.Name)
		}
	}

	containerName := "honey-debug-" + generateID()
	targetContainer := ""
	if len(pod.Spec.Containers) > 0 {
		targetContainer = pod.Spec.Containers[0].Name
	}

	pod.Spec.EphemeralContainers = append(pod.Spec.EphemeralContainers, corev1.EphemeralContainer{
		EphemeralContainerCommon: corev1.EphemeralContainerCommon{
			Name:            containerName,
			Image:           image,
			Command:         []string{"sh", "-c", "sleep infinity"},
			ImagePullPolicy: corev1.PullIfNotPresent,
		},
		TargetContainerName: targetContainer,
	})

	_, err = clientset.CoreV1().Pods(namespace).UpdateEphemeralContainers(ctx, podName, pod, metav1.UpdateOptions{})
	if err != nil {
		return "", fmt.Errorf("update ephemeral containers: %w", err)
	}

	if err := waitForEphemeralContainer(ctx, clientset, namespace, podName, containerName); err != nil {
		return "", err
	}

	return containerName, nil
}

func waitForEphemeralContainer(ctx context.Context, clientset kubernetes.Interface, namespace, podName, containerName string) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	watcher, err := clientset.CoreV1().Pods(namespace).Watch(timeoutCtx, metav1.ListOptions{
		FieldSelector: "metadata.name=" + podName,
	})
	if err != nil {
		return fmt.Errorf("watch pod: %w", err)
	}
	defer watcher.Stop()

	// Check if already running before waiting
	pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err == nil {
		if isContainerRunning(pod, containerName) {
			return nil
		}
	}

	for {
		select {
		case <-timeoutCtx.Done():
			return fmt.Errorf("waiting for ephemeral container %s: %w", containerName, timeoutCtx.Err())
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return fmt.Errorf("ephemeral container %s did not become ready", containerName)
			}
			p, ok := event.Object.(*corev1.Pod)
			if !ok {
				continue
			}
			if isContainerRunning(p, containerName) {
				return nil
			}
		}
	}
}

func isContainerRunning(pod *corev1.Pod, containerName string) bool {
	for _, s := range pod.Status.EphemeralContainerStatuses {
		if s.Name == containerName && s.State.Running != nil {
			return true
		}
	}
	return false
}
