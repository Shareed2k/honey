package k8sprovider

import "github.com/spf13/cobra"

var cliFlags struct {
	context    string
	kubeconfig string
	mode       string
	debugImage string
}

// RegisterFlags adds Kubernetes CLI flags to cmd.
func RegisterFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&cliFlags.context, "kube-context", "", "Kubernetes context override")
	cmd.Flags().StringVar(&cliFlags.kubeconfig, "kubeconfig", "", "Path to kubeconfig file")
	cmd.Flags().StringVar(&cliFlags.mode, "k8s-mode", "nodes", "Kubernetes search mode: nodes or pods")
	cmd.Flags().StringVar(&cliFlags.debugImage, "k8s-debug-image", "", "Container image used for ephemeral debug containers (default: alpine:3.23)")
}
