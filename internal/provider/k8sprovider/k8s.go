package k8sprovider

import (
	"context"
	"fmt"
	"honey/internal/hosts"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// K8s resolves node or pod addresses.
type K8s struct {
	Name           string // optional config label (--backends)
	KubeconfigPath string
	Context        string
	// Mode is the default k8s mode (nodes|pods) when q.K8sMode is empty.
	Mode string
}

// ID returns the honey backend identifier ("k8s").
func (K8s) ID() string { return "k8s" }

// BackendName returns the optional YAML backends.kubernetes[].name value.
func (k *K8s) BackendName() string { return strings.TrimSpace(k.Name) }

// CacheIdentity scopes cache entries per kubeconfig/context/mode.
func (k *K8s) CacheIdentity() string {
	mode := k.Mode
	if mode == "" {
		mode = "nodes"
	}
	return strings.TrimSpace(k.Name) + "\x1e" + k.KubeconfigPath + "\x1e" + k.Context + "\x1e" + mode
}

var _ hosts.Backend = (*K8s)(nil)

// Search returns Kubernetes nodes or pods matching the query.
func (k *K8s) Search(ctx context.Context, q hosts.Query) ([]hosts.Record, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	kubePath := k.KubeconfigPath
	if kubePath != "" {
		loadingRules.ExplicitPath = kubePath
	}
	overrides := &clientcmd.ConfigOverrides{}
	ctxName := k.Context
	if q.KubeContext != "" {
		ctxName = q.KubeContext
	}
	if ctxName != "" {
		overrides.CurrentContext = ctxName
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
		mode = k.Mode
	}
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
		extras = append(extras[:0], ext[1:]...)
		extras = append(extras, internal...)
		return primary, extras
	}
	if len(internal) > 0 {
		primary = internal[0]
		suffix := append([]string(nil), extras...)
		extras = append(extras[:0], internal[1:]...)
		extras = append(extras, suffix...)
		return primary, extras
	}
	return "", nil
}
