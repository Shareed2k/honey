package k8sprovider

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestSearchPodsMeta(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "worker-node-7"},
			Status: corev1.NodeStatus{
				Addresses: []corev1.NodeAddress{
					{Type: corev1.NodeInternalIP, Address: "10.0.0.5"},
					{Type: corev1.NodeInternalIP, Address: "10.0.0.6"},
					// Hostname must not appear in IP extras or node_extra_ips (duplicates node name).
					{Type: corev1.NodeHostName, Address: "worker-node-7"},
				},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
			},
			Spec: corev1.PodSpec{
				NodeName: "worker-node-7",
			},
			Status: corev1.PodStatus{
				PodIP: "1.2.3.4",
			},
		},
	)

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
	if r.Meta["node"] != "worker-node-7" {
		t.Errorf("expected node=worker-node-7, got %q", r.Meta["node"])
	}
	if r.Meta["node_ip"] != "10.0.0.5" {
		t.Errorf("expected node_ip=10.0.0.5, got %q", r.Meta["node_ip"])
	}
	if r.Meta["node_extra_ips"] != "10.0.0.6" {
		t.Errorf("expected node_extra_ips=10.0.0.6, got %q", r.Meta["node_extra_ips"])
	}
	wantExtras := []string{"worker-node-7", "10.0.0.5", "10.0.0.6"}
	if len(r.ExtraIPs) != len(wantExtras) {
		t.Fatalf("ExtraIPs: want %v got %#v", wantExtras, r.ExtraIPs)
	}
	for i, w := range wantExtras {
		if r.ExtraIPs[i] != w {
			t.Errorf("ExtraIPs[%d]: want %q got %q", i, w, r.ExtraIPs[i])
		}
	}
}
