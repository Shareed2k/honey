package main

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func main() {
	clientset := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"},
	})
	
	pod, _ := clientset.CoreV1().Pods("default").Get(context.Background(), "test-pod", metav1.GetOptions{})
	
	pod.Spec.EphemeralContainers = []corev1.EphemeralContainer{
		{
			EphemeralContainerCommon: corev1.EphemeralContainerCommon{
				Name: "hostctl-debug",
				Image: "alpine:3.19",
			},
		},
	}
	
	_, err := clientset.CoreV1().Pods("default").UpdateEphemeralContainers(context.Background(), pod.Name, pod, metav1.UpdateOptions{})
	fmt.Printf("Update err: %v\n", err)
}
