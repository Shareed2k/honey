package interceptwire

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// PortForwarder opens client-go SPDY port-forwards to a target pod, binding
// each to an ephemeral 127.0.0.1 port. It satisfies intercept.PortForwarder.
type PortForwarder struct {
	Cfg *rest.Config
}

// Forward establishes a port-forward to remotePort on the pod and returns the
// bound local address, a stop function safe to call once, and any setup error.
// The cluster argument is unused: the REST config already targets one cluster.
func (pf *PortForwarder) Forward(ctx context.Context, _, namespace, pod string, remotePort int) (string, func(), error) {
	reqURL, err := url.Parse(fmt.Sprintf("%s/api/v1/namespaces/%s/pods/%s/portforward", pf.Cfg.Host, namespace, pod))
	if err != nil {
		return "", nil, fmt.Errorf("intercept: build port-forward url: %w", err)
	}
	transport, upgrader, err := spdy.RoundTripperFor(pf.Cfg)
	if err != nil {
		return "", nil, fmt.Errorf("intercept: spdy round tripper: %w", err)
	}
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, reqURL)

	stopCh := make(chan struct{})
	readyCh := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(stopCh) }) }

	fw, err := portforward.New(dialer, []string{fmt.Sprintf("0:%d", remotePort)}, stopCh, readyCh, io.Discard, io.Discard)
	if err != nil {
		stop()
		return "", nil, fmt.Errorf("intercept: create port forwarder: %w", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- fw.ForwardPorts() }()

	select {
	case <-readyCh:
	case err := <-errCh:
		stop()
		return "", nil, fmt.Errorf("intercept: port-forward to %s/%s: %w", namespace, pod, err)
	case <-ctx.Done():
		stop()
		return "", nil, fmt.Errorf("intercept: port-forward to %s/%s: %w", namespace, pod, ctx.Err())
	}

	ports, err := fw.GetPorts()
	if err != nil || len(ports) == 0 {
		stop()
		return "", nil, fmt.Errorf("intercept: resolve local port: %w", err)
	}
	return fmt.Sprintf("127.0.0.1:%d", ports[0].Local), stop, nil
}
