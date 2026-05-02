package k8sprovider

import (
	"context"
	"honey/internal/hosts"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestSearchPodsMeta(t *testing.T) {
	clientset := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Status: corev1.PodStatus{
			PodIP: "1.2.3.4",
		},
	})

	k := &K8s{
		Name: "my-backend",
	}

	q := hosts.Query{}
	records, err := k.searchPods(context.Background(), clientset, q, "my-context", "my-kubeconfig")
	if err != nil {
		t.Fatalf("searchPods failed: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	r := records[0]
	if r.Meta["kind"] != "pod" {
		t.Errorf("expected kind=pod, got %q", r.Meta["kind"])
	}
	if r.Meta["namespace"] != "default" {
		t.Errorf("expected namespace=default, got %q", r.Meta["namespace"])
	}
	if r.Meta["pod_name"] != "test-pod" {
		t.Errorf("expected pod_name=test-pod, got %q", r.Meta["pod_name"])
	}
	if r.Meta["kube_context"] != "my-context" {
		t.Errorf("expected kube_context=my-context, got %q", r.Meta["kube_context"])
	}
	if r.Meta["kubeconfig"] != "my-kubeconfig" {
		t.Errorf("expected kubeconfig=my-kubeconfig, got %q", r.Meta["kubeconfig"])
	}
	if r.Meta["backend_name"] != "my-backend" {
		t.Errorf("expected backend_name=my-backend, got %q", r.Meta["backend_name"])
	}
}
