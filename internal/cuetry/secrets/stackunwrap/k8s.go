package stackunwrap

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// K8s loads the stack data key from a Kubernetes Secret data field.
// secretsprovider: k8s://namespace/secretName
// encryptedkey: data key name (value is raw 32 bytes or base64).
type K8s struct{}

// Name implements [DataKeyUnwrapper].
func (K8s) Name() string { return "k8s" }

// Supports implements [DataKeyUnwrapper].
func (K8s) Supports(providerURL string) bool {
	p := strings.TrimSpace(providerURL)
	return strings.HasPrefix(p, "k8s://")
}

func (K8s) Unwrap(ctx context.Context, providerURL, encryptedKey string) ([]byte, error) {
	rest := strings.TrimSpace(providerURL[len("k8s://"):])
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("k8s stack provider must be k8s://namespace/secretName")
	}
	ns, name := parts[0], parts[1]
	field := strings.TrimSpace(encryptedKey)
	if ns == "" || name == "" || field == "" {
		return nil, fmt.Errorf("k8s stack provider requires namespace, secret name, and encryptedkey as data key name")
	}
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		home, _ := os.UserHomeDir()
		kubeconfig = filepath.Join(home, ".kube", "config")
	}
	cc, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("k8s kubeconfig: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(cc)
	if err != nil {
		return nil, err
	}
	sec, err := clientset.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("k8s get secret %s/%s: %w", ns, name, err)
	}
	raw, ok := sec.Data[field]
	if !ok {
		return nil, fmt.Errorf("k8s: key %q not in secret %s/%s", field, ns, name)
	}
	if b, err := base64.StdEncoding.DecodeString(string(raw)); err == nil && len(b) > 0 {
		return b, nil
	}
	return raw, nil
}
