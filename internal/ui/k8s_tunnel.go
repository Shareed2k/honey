package ui

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	"github.com/shareed2k/honey/internal/hosts"
)

func (k *k8sPodExecutor) RunTunnel(ctx context.Context, _ string, r hosts.Record, localFwd string, out io.Writer) error {
	namespace := r.Meta["namespace"]
	podName := r.Meta["pod_name"]
	kubeContext := r.Meta["kube_context"]
	kubeconfig := r.Meta["kubeconfig"]

	if namespace == "" || podName == "" {
		return fmt.Errorf("missing k8s namespace or pod_name in metadata")
	}

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		loadingRules.ExplicitPath = kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{}
	if kubeContext != "" {
		overrides.CurrentContext = kubeContext
	}
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)

	cfg, err := cc.ClientConfig()
	if err != nil {
		return fmt.Errorf("k8s config: %w", err)
	}

	// Parse localFwd format "localPort:remotePort" (in SSH it's localPort:remoteHost:remotePort, but in k8s we only forward to the pod itself)
	// We'll normalize it for k8s port-forward format: "local:remote" or just "port"
	parts := strings.Split(localFwd, ":")
	var ports []string
	switch len(parts) {
	case 3:
		// e.g. "8080:localhost:80" -> use "8080:80"
		ports = []string{fmt.Sprintf("%s:%s", parts[0], parts[2])}
	default:
		// e.g. "8080:80" or "8080"
		ports = []string{localFwd}
	}

	reqURL, err := url.Parse(fmt.Sprintf("%s/api/v1/namespaces/%s/pods/%s/portforward", cfg.Host, namespace, podName))
	if err != nil {
		return err
	}

	transport, upgrader, err := spdy.RoundTripperFor(cfg)
	if err != nil {
		return fmt.Errorf("spdy round tripper: %w", err)
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", reqURL)

	stopCh := make(chan struct{}, 1)
	readyCh := make(chan struct{})

	// Handle cancellation via context
	go func() {
		<-ctx.Done()
		close(stopCh)
	}()

	fw, err := portforward.New(dialer, ports, stopCh, readyCh, out, out)
	if err != nil {
		return fmt.Errorf("create port forwarder: %w", err)
	}

	fmt.Fprintf(out, "\r\n[honey] Forwarding %s -> Pod %s in namespace %s (Ctrl+C to stop)\n", strings.Join(ports, ", "), podName, namespace)
	return fw.ForwardPorts()
}
