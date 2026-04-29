package k8sprovider

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"hostctl/internal/hosts"
)

// K8s resolves node or pod addresses.
type K8s struct {
	KubeconfigPath string
}

func (K8s) ID() string { return "k8s" }

var _ hosts.Backend = (*K8s)(nil)

func (k *K8s) Search(ctx context.Context, q hosts.Query) ([]hosts.Record, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if k.KubeconfigPath != "" {
		loadingRules.ExplicitPath = k.KubeconfigPath
	}
	overrides := &clientcmd.ConfigOverrides{}
	if q.KubeContext != "" {
		overrides.CurrentContext = q.KubeContext
	}
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)
	cfg, err := cc.ClientConfig()
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}

	mode := q.K8sMode
	if mode == "" {
		mode = "nodes"
	}
	switch mode {
	case "nodes":
		return k.searchNodes(ctx, clientset, q)
	case "pods":
		return k.searchPods(ctx, clientset, q)
	default:
		return nil, fmt.Errorf("unsupported k8s mode %q (use nodes or pods)", mode)
	}
}

func (k *K8s) searchNodes(ctx context.Context, clientset *kubernetes.Clientset, q hosts.Query) ([]hosts.Record, error) {
	list, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var out []hosts.Record
	for _, n := range list.Items {
		ok, err := hosts.NameMatches(n.Name, q)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		primary, extras := nodeIPs(n)
		if primary == "" {
			continue
		}
		out = append(out, hosts.Record{
			Provider:  "k8s",
			Name:      n.Name,
			PrimaryIP: primary,
			ExtraIPs:  extras,
			Zone:      nodeZone(n),
			Region:    "",
			Meta: map[string]string{
				"kind": "node",
			},
		})
	}
	return out, nil
}

func (k *K8s) searchPods(ctx context.Context, clientset *kubernetes.Clientset, q hosts.Query) ([]hosts.Record, error) {
	list, err := clientset.CoreV1().Pods(corev1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var out []hosts.Record
	for _, p := range list.Items {
		ok, err := hosts.NameMatches(p.Name, q)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		ip := p.Status.PodIP
		if ip == "" {
			continue
		}
		ns := p.Namespace
		out = append(out, hosts.Record{
			Provider:  "k8s",
			Name:      fmt.Sprintf("%s/%s", ns, p.Name),
			PrimaryIP: ip,
			ExtraIPs:  nil,
			Zone:      "",
			Region:    "",
			Meta: map[string]string{
				"kind":      "pod",
				"namespace": ns,
			},
		})
	}
	return out, nil
}

func nodeZone(n corev1.Node) string {
	if z, ok := n.Labels["topology.kubernetes.io/zone"]; ok {
		return z
	}
	if z, ok := n.Labels["failure-domain.beta.kubernetes.io/zone"]; ok {
		return z
	}
	return ""
}

func nodeIPs(n corev1.Node) (primary string, extras []string) {
	var ext, internal []string
	for _, a := range n.Status.Addresses {
		switch a.Type {
		case corev1.NodeExternalIP:
			ext = append(ext, a.Address)
		case corev1.NodeInternalIP:
			internal = append(internal, a.Address)
		default:
			if a.Address != "" {
				extras = append(extras, a.Address)
			}
		}
	}
	if len(ext) > 0 {
		primary = ext[0]
		extras = append(ext[1:], internal...)
		return primary, extras
	}
	if len(internal) > 0 {
		primary = internal[0]
		extras = append(internal[1:], extras...)
		return primary, extras
	}
	return "", nil
}
