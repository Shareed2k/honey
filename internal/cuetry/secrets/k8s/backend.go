// Package k8s resolves Kubernetes Secret data keys.
package k8s

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/shareed2k/honey/internal/cuetry/secrets/ref"
)

// Backend implements [ref.Backend] for k8s:namespace/name/key.
type Backend struct{}

// New returns a Kubernetes secrets backend.
func New() ref.Backend { return Backend{} }

// Name implements [ref.Backend].
func (Backend) Name() string { return "k8s" }

// Handles implements [ref.Backend].
func (Backend) Handles(ref string) bool {
	return strings.HasPrefix(strings.TrimSpace(ref), "k8s:")
}

// Resolve implements [ref.Backend].
func (Backend) Resolve(ctx context.Context, ref string) (string, error) {
	r := strings.TrimSpace(ref[len("k8s:"):])
	parts := strings.Split(r, "/")
	if len(parts) != 3 {
		return "", fmt.Errorf("k8s ref must be k8s:namespace/secretName/dataKey")
	}
	ns, name, key := parts[0], parts[1], parts[2]
	if ns == "" || name == "" || key == "" {
		return "", fmt.Errorf("k8s: namespace, name, and key required")
	}
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		home, _ := os.UserHomeDir()
		kubeconfig = filepath.Join(home, ".kube", "config")
	}
	cc, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return "", fmt.Errorf("k8s kubeconfig: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(cc)
	if err != nil {
		return "", err
	}
	sec, err := clientset.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("k8s get secret %s/%s: %w", ns, name, err)
	}
	v, ok := sec.Data[key]
	if !ok {
		return "", fmt.Errorf("k8s: key %q not in secret %s/%s", key, ns, name)
	}
	return string(v), nil
}
