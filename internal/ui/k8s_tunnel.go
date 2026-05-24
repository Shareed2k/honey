package ui

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
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

// StartK8sPortForward starts a non-blocking k8s port-forward; returns local listen host/port after readyCh.
func StartK8sPortForward(ctx context.Context, r hosts.Record, localPort, remotePort int) (host string, port int, stop func(), err error) {
	namespace := r.Meta["namespace"]
	podName := r.Meta["pod_name"]
	kubeContext := r.Meta["kube_context"]
	kubeconfig := r.Meta["kubeconfig"]
	if namespace == "" || podName == "" {
		return "", 0, nil, fmt.Errorf("missing k8s namespace or pod_name in metadata")
	}
	if remotePort <= 0 {
		return "", 0, nil, fmt.Errorf("remote_port is required for k8s tunnel")
	}
	if localPort == 0 {
		ln, lerr := net.Listen("tcp", "127.0.0.1:0")
		if lerr != nil {
			return "", 0, nil, lerr
		}
		addr := ln.Addr().String()
		_ = ln.Close()
		_, portStr, _ := net.SplitHostPort(addr)
		localPort, _ = strconv.Atoi(portStr)
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
		return "", 0, nil, fmt.Errorf("k8s config: %w", err)
	}
	ports := []string{fmt.Sprintf("%d:%d", localPort, remotePort)}
	reqURL, err := url.Parse(fmt.Sprintf("%s/api/v1/namespaces/%s/pods/%s/portforward", cfg.Host, namespace, podName))
	if err != nil {
		return "", 0, nil, err
	}
	transport, upgrader, err := spdy.RoundTripperFor(cfg)
	if err != nil {
		return "", 0, nil, fmt.Errorf("spdy round tripper: %w", err)
	}
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", reqURL)
	stopCh := make(chan struct{}, 1)
	readyCh := make(chan struct{})
	runCtx, cancel := context.WithCancel(ctx)
	go func() {
		<-runCtx.Done()
		close(stopCh)
	}()
	fw, err := portforward.New(dialer, ports, stopCh, readyCh, io.Discard, io.Discard)
	if err != nil {
		cancel()
		return "", 0, nil, fmt.Errorf("create port forwarder: %w", err)
	}
	go func() {
		_ = fw.ForwardPorts()
	}()
	select {
	case <-readyCh:
	case <-runCtx.Done():
		cancel()
		return "", 0, nil, runCtx.Err()
	}
	stopFn := func() { cancel() }
	return "127.0.0.1", localPort, stopFn, nil
}
