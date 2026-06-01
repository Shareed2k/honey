package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/template"

	"github.com/prometheus/alertmanager/notify/webhook"
	amtemplate "github.com/prometheus/alertmanager/template"
	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/ui"
)

var (
	flagAlertLabels []string
	flagAlertStdin  bool
)

var alertCmd = &cobra.Command{
	Use:   "alert",
	Short: "Alert investigation tools",
}

var alertInvestigateCmd = &cobra.Command{
	Use:   "investigate",
	Short: "Investigate a firing alert: resolve host and open TUI",
	Long: `Parse an Alertmanager alert (via --label flags or --stdin JSON), map it to a
honey host using alert_mappings from config, and open the TUI pre-filtered to that host.

Examples:
  honey alert investigate --label alertname=PostgreSQLReplicationLag --label cluster=postgres-main
  echo '<alertmanager-json>' | honey alert investigate --stdin`,
	RunE: runAlertInvestigate,
}

func init() {
	alertInvestigateCmd.Flags().StringArrayVar(&flagAlertLabels, "label", nil, "Alert label as key=value (repeatable)")
	alertInvestigateCmd.Flags().BoolVar(&flagAlertStdin, "stdin", false, "Read Alertmanager JSON webhook payload from stdin")
	addSearchCoreFlags(alertInvestigateCmd)
	alertCmd.AddCommand(alertInvestigateCmd)
	alertCmd.AddCommand(alertServeCmd)
}

// parseAlertMessage builds a webhook.Message from --label flags or stdin JSON.
func parseAlertMessage() (*webhook.Message, error) {
	if flagAlertStdin {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("reading stdin: %w", err)
		}
		data = bytes.TrimSpace(data)
		var msg webhook.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, fmt.Errorf("parsing alertmanager JSON: %w", err)
		}
		return &msg, nil
	}

	// Build a synthetic single-alert message from --label flags.
	labels := make(amtemplate.KV)
	for _, kv := range flagAlertLabels {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("invalid --label %q: expected key=value", kv)
		}
		labels[k] = v
	}
	msg := &webhook.Message{
		Data: &amtemplate.Data{
			Status: "firing",
			Alerts: amtemplate.Alerts{
				{Labels: labels, Status: "firing"},
			},
		},
		Version: "4",
	}
	return msg, nil
}

// firstFiringAlert returns the first alert with status "firing" in the message.
func firstFiringAlert(msg *webhook.Message) *amtemplate.Alert {
	if msg == nil || msg.Data == nil {
		return nil
	}
	for i := range msg.Alerts {
		if msg.Alerts[i].Status == "firing" {
			return &msg.Alerts[i]
		}
	}
	return nil
}

// resolveAlertMapping finds the first AlertMapping whose MatchLabels are all present in labels.
func resolveAlertMapping(cfg *config.File, labels amtemplate.KV) *config.AlertMapping {
	if cfg == nil {
		return nil
	}
	for i := range cfg.AlertMappings {
		m := &cfg.AlertMappings[i]
		match := true
		for k, v := range m.MatchLabels {
			if labels[k] != v {
				match = false
				break
			}
		}
		if match {
			return m
		}
	}
	return nil
}

// evalHostQuery executes the HostQuery Go template against alert labels.
func evalHostQuery(hostQuery string, labels amtemplate.KV) (string, error) {
	tmpl, err := template.New("").Option("missingkey=zero").Parse(hostQuery)
	if err != nil {
		return "", fmt.Errorf("host_query template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]string(labels)); err != nil {
		return "", fmt.Errorf("host_query evaluate: %w", err)
	}
	return strings.TrimSpace(buf.String()), nil
}

func runAlertInvestigate(cmd *cobra.Command, args []string) error {
	if !flagAlertStdin && len(flagAlertLabels) == 0 {
		return fmt.Errorf("provide --label key=value flags or --stdin to supply alert data")
	}

	msg, err := parseAlertMessage()
	if err != nil {
		return err
	}

	alert := firstFiringAlert(msg)
	if alert == nil {
		fmt.Fprintln(os.Stderr, "No firing alerts in payload.")
		return nil
	}

	labels := alert.Labels
	cfg := resolvedCfg

	// Find matching alert mapping.
	mapping := resolveAlertMapping(cfg, labels)

	var hostQuery string
	if mapping != nil {
		hostQuery, err = evalHostQuery(mapping.HostQuery, labels)
		if err != nil {
			return err
		}
	} else {
		// No mapping: try cluster label as fallback, then environment, then alertname.
		for _, k := range []string{"cluster", "job", "instance", "hostname", "environment"} {
			if v := strings.TrimSpace(labels[k]); v != "" {
				hostQuery = v
				break
			}
		}
		if hostQuery == "" {
			fmt.Fprintf(os.Stderr, "No alert_mappings matched. Labels: %v\n", labels)
			fmt.Fprintf(os.Stderr, "Try: honey search <host-name>\n")
			return nil
		}
		fmt.Fprintf(os.Stderr, "No alert_mappings matched. Searching by %q (add alert_mappings to config for precise control).\n", hostQuery)
	}

	// Build alert banner.
	alertname := labels["alertname"]
	severity := labels["severity"]
	env := labels["environment"]
	cluster := labels["cluster"]
	banner := buildAlertBanner(alertname, severity, env, cluster)

	// Print mapping hints to stderr (not TUI).
	if mapping != nil && mapping.Recipe != "" {
		fmt.Fprintf(os.Stderr, "Suggested recipe: honey cue-exec %s %q --execute\n", mapping.Recipe, hostQuery)
	}
	if mapping != nil && mapping.Command != "" {
		fmt.Fprintf(os.Stderr, "Suggested command: honey exec %q %q\n", hostQuery, mapping.Command)
	}

	// Run search and open TUI pre-filtered to the resolved host query.
	clientCache := ui.NewClientCache()

	// Set the name filter to the resolved host query for runSearchCore.
	if !cmd.Flags().Changed("name") {
		flagName = hostQuery
	}

	records, sshUser, cfg2, cfgPath, err := runSearchCore(cmd, args)
	if err != nil {
		clientCache.CloseAll()
		return err
	}

	reg := buildHostExecRegistry()
	reg.Reconfigure(cfg2)
	clientCache.SetRegistry(reg)

	if len(records) == 0 {
		clientCache.CloseAll()
		fmt.Fprintf(os.Stderr, "No hosts found for query %q.\n", hostQuery)
		fmt.Fprintf(os.Stderr, "Try: honey search %s\n", hostQuery)
		return nil
	}

	recordDir := config.ResolveRecordDir(cfg2, cfgPath, flagRecordDir, recordDirFlagChanged(cmd))
	return ui.RunTable(records, sshUser, ui.RunTableOptions{
		RecordDir:   recordDir,
		Config:      cfg2,
		ConfigPath:  cfgPath,
		ClientCache: clientCache,
		AlertBanner: banner,
	})
}

func buildAlertBanner(alertname, severity, env, cluster string) string {
	var parts []string
	if alertname != "" {
		parts = append(parts, alertname)
	}
	if severity != "" {
		parts = append(parts, "severity="+severity)
	}
	if env != "" {
		parts = append(parts, "env="+env)
	}
	if cluster != "" {
		parts = append(parts, "cluster="+cluster)
	}
	return strings.Join(parts, "  ")
}
