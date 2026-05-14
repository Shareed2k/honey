package k8sprovider

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/shareed2k/honey/internal/hosts"
)

// K8s resolves node or pod addresses.
type K8s struct {
	Name           string // optional config label (--backends)
	KubeconfigPath string
	Context        string
	// Mode is the default k8s mode (nodes|pods) when q.K8sMode is empty.
	Mode string
	// DebugImage is the default container image when q.K8sDebugImage is empty.
	DebugImage string
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

	zap.L().Debug("k8s starting search", zap.String("context", ctxName), zap.String("mode", mode))

	switch mode {
	case "nodes":
		return k.searchNodes(ctx, clientset, q)
	case "pods":
		rawConfig, _ := cc.RawConfig()
		resolvedContext := ctxName
		if resolvedContext == "" {
			resolvedContext = rawConfig.CurrentContext
		}
		return k.searchPods(ctx, clientset, q, resolvedContext, kubePath)
	default:
		return nil, fmt.Errorf("unsupported k8s mode %q (use nodes or pods)", mode)
	}
}

func (k *K8s) searchNodes(ctx context.Context, clientset kubernetes.Interface, q hosts.Query) ([]hosts.Record, error) {
	list, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]hosts.Record, 0, len(list.Items))
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
		meta := map[string]string{
			"kind": "node",
		}

		for k, v := range n.Labels {
			meta["label_"+k] = v
		}
		for k, v := range n.Annotations {
			if k == "kubectl.kubernetes.io/last-applied-configuration" {
				continue
			}
			meta["annotation_"+k] = v
		}

		out = append(out, hosts.Record{
			Provider:  "k8s",
			Name:      n.Name,
			PrimaryIP: primary,
			ExtraIPs:  append([]string(nil), extras...),
			Zone:      nodeZone(n),
			Region:    "",
			Meta:      meta,
		})
	}
	return out, nil
}

// nodeAddrInfo holds one node's reachable addresses (same rules as searchNodes).
type nodeAddrInfo struct {
	primary string
	extras  []string
}

func nodeAddressIndex(ctx context.Context, clientset kubernetes.Interface) map[string]nodeAddrInfo {
	nodeList, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		zap.L().Warn("k8s pods search: could not list nodes; pod extras will omit node IPs (RBAC or API error)",
			zap.Error(err))
		return nil
	}
	out := make(map[string]nodeAddrInfo, len(nodeList.Items))
	for i := range nodeList.Items {
		n := &nodeList.Items[i]
		pri, ex := nodeIPs(*n)
		if pri == "" {
			continue
		}
		// Own backing so pod rows never share slices with the node index or each other.
		exCopy := append([]string(nil), ex...)
		out[n.Name] = nodeAddrInfo{primary: pri, extras: exCopy}
	}
	return out
}

func (k *K8s) searchPods(ctx context.Context, clientset kubernetes.Interface, q hosts.Query, resolvedContext string, kubeconfig string) ([]hosts.Record, error) {
	list, err := clientset.CoreV1().Pods(corev1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	nodeIndex := nodeAddressIndex(ctx, clientset)
	out := make([]hosts.Record, 0, len(list.Items))
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
		nodeName := strings.TrimSpace(p.Spec.NodeName)

		portSet := make(map[int32]struct{})
		var uniquePorts []string
		for _, c := range p.Spec.Containers {
			for _, port := range c.Ports {
				if port.ContainerPort > 0 {
					if _, ok := portSet[port.ContainerPort]; !ok {
						portSet[port.ContainerPort] = struct{}{}
						uniquePorts = append(uniquePorts, fmt.Sprintf("%d", port.ContainerPort))
					}
				}
			}
		}
		var portString string
		if len(uniquePorts) > 0 {
			portString = strings.Join(uniquePorts, ",")
		}

		meta := map[string]string{
			"kind":         "pod",
			"namespace":    ns,
			"pod_name":     p.Name,
			"kube_context": resolvedContext,
			"kubeconfig":   kubeconfig,
			"backend_name": k.BackendName(),
		}
		if portString != "" {
			meta["ports"] = portString
		}

		for k, v := range p.Labels {
			meta["label_"+k] = v
		}
		for k, v := range p.Annotations {
			if k == "kubectl.kubernetes.io/last-applied-configuration" {
				continue
			}
			meta["annotation_"+k] = v
		}

		if nodeName != "" {
			meta["node"] = nodeName
			if nodeIndex != nil {
				if na, ok := nodeIndex[nodeName]; ok {
					meta["node_ip"] = na.primary
					if len(na.extras) > 0 {
						meta["node_extra_ips"] = strings.Join(na.extras, ",")
					}
				}
			}
		}
		img := q.K8sDebugImage
		if img == "" {
			img = k.DebugImage
		}
		if img != "" {
			meta["debug_image"] = img
		}
		var extras []string
		if nodeName != "" {
			extras = append(extras, nodeName)
			if nodeIndex != nil {
				if na, ok := nodeIndex[nodeName]; ok {
					extras = append(extras, na.primary)
					extras = append(extras, na.extras...)
				}
			}
		}
		extras = append([]string(nil), extras...)
		out = append(out, hosts.Record{
			Provider:  "k8s",
			Name:      fmt.Sprintf("%s/%s", ns, p.Name),
			PrimaryIP: ip,
			ExtraIPs:  extras,
			Zone:      "",
			Region:    "",
			Meta:      meta,
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
			// Hostname duplicates the node name and is not an extra IP.
			if a.Address != "" && a.Type != corev1.NodeHostName {
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
