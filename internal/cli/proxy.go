package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/apps"
	"github.com/shareed2k/honey/internal/proxy"
)

var proxyCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Manage active proxy sessions",
}

var proxyTCPCmd = &cobra.Command{
	Use:   "tcp <name>",
	Short: "Start a TCP proxy for a configured app",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		return runProxyApp(cmd, name, apps.AppTypeTCP)
	},
}

var proxyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List active proxy sessions",
	RunE: func(_ *cobra.Command, _ []string) error {
		mgr := proxy.NewManager(proxy.NewLogger(zap.L()))
		sessions, err := mgr.List()
		if err != nil {
			return err
		}

		if len(sessions) == 0 {
			fmt.Println("No active proxy sessions.")
			return nil
		}

		fmt.Printf("%-10s %-20s %-25s %-25s %-10s\n", "ID", "APP", "LOCAL ADDR", "UPSTREAM", "PID")
		for _, s := range sessions {
			fmt.Printf("%-10s %-20s %-25s %-25s %-10d\n", s.ID, s.App.Name, s.LocalAddr, s.App.Upstream, s.PID)
		}
		return nil
	},
}

var proxyStopCmd = &cobra.Command{
	Use:   "stop <id>",
	Short: "Stop an active proxy session",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		id := args[0]
		mgr := proxy.NewManager(proxy.NewLogger(zap.L()))

		if err := mgr.Stop(id); err != nil {
			return fmt.Errorf("failed to stop session %s: %w", id, err)
		}

		fmt.Printf("Session %s stopped successfully.\n", id)
		// Give process time to receive signal
		time.Sleep(200 * time.Millisecond)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(proxyCmd)
	proxyCmd.AddCommand(proxyTCPCmd)
	proxyCmd.AddCommand(proxyListCmd)
	proxyCmd.AddCommand(proxyStopCmd)

	proxyTCPCmd.Flags().IntVar(&flagAppPort, "port", 0, "Override local port")
	proxyTCPCmd.Flags().BoolVar(&flagAppPrintURL, "print-url", false, "Print only the address")
	proxyTCPCmd.Flags().StringVar(&flagConfig, "config", "", "Path to honey YAML (optional; also HONEY_CONFIG or default paths in README)")
	proxyTCPCmd.Flags().StringVar(&flagProviders, "provider", "", "Comma-separated: gcp,aws,k8s,consul,proxmox,truenas,docker,local (default: all)")
	proxyTCPCmd.Flags().StringVar(&flagBackends, "backends", "", "Comma-separated backend names (YAML backends.*.name); only those entries run")
	proxyTCPCmd.Flags().StringVar(&flagSSHUser, "ssh-user", "", "Default SSH user for connect actions (defaults to config or OS user)")
	proxyTCPCmd.Flags().StringVar(&flagGCPProject, "gcp-project", "", "GCP project (or GOOGLE_CLOUD_PROJECT / GCP_PROJECT)")
	proxyTCPCmd.Flags().StringVar(&flagGCPZone, "gcp-zone", "", "Limit GCP to a single zone (default: all zones)")
	proxyTCPCmd.Flags().StringVar(&flagAWSProfile, "aws-profile", "", "AWS shared config profile")
	proxyTCPCmd.Flags().StringVar(&flagAWSRegion, "aws-region", "", "AWS region (default: from profile/env)")
	proxyTCPCmd.Flags().StringVar(&flagKubeContext, "kube-context", "", "Kubernetes context override")
	proxyTCPCmd.Flags().StringVar(&flagKubeconfig, "kubeconfig", "", "Path to kubeconfig file")
	proxyTCPCmd.Flags().StringVar(&flagK8sMode, "k8s-mode", "nodes", "Kubernetes search mode: nodes or pods")
}
