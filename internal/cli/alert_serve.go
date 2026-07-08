package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/alertwebhook"
)

var (
	flagServePort  int
	flagServeToken string
)

var alertServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start Alertmanager webhook receiver",
	Long: `Start an HTTP server that receives Alertmanager webhook payloads,
deduplicates alerts, resolves matching hosts via alert_mappings, runs
investigation commands via SSH, and notifies configured channels.

Configure Alertmanager receiver:
  receivers:
    - name: honey
      webhook_configs:
        - url: http://honey-host:9095/webhook/alert
          http_config:
            bearer_token: "my-secret-token"`,
	RunE: runAlertServe,
}

func init() {
	alertServeCmd.Flags().IntVar(&flagServePort, "port", 0, "Override config alert_webhook.port (default 9095)")
	alertServeCmd.Flags().StringVar(&flagServeToken, "token", "", "Override config alert_webhook.token")
}

func runAlertServe(cmd *cobra.Command, _ []string) error {
	cfg := resolvedCfg
	whCfg := alertwebhook.DefaultConfig()
	if cfg != nil {
		whCfg = cfg.AlertWebhook
	}
	if cmd.Flags().Changed("port") {
		whCfg.Port = flagServePort
	}
	if cmd.Flags().Changed("token") {
		whCfg.Token = flagServeToken
	}
	if whCfg.Port == 0 {
		whCfg.Port = 9095
	}

	srv, err := alertwebhook.New(whCfg, cfg, resolvedCfgPath, GetSearchRegistry())
	if err != nil {
		return fmt.Errorf("alert webhook: %w", err)
	}
	return srv.ListenAndServe(cmd.Context())
}
