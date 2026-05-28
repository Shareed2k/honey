package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/apps"
	"github.com/shareed2k/honey/internal/macros"
)

var (
	flagMacrosFile   string
	flagMacrosList   bool
	flagMacrosDryRun bool
	flagMacrosOutput string
)

var macrosCmd = &cobra.Command{
	Use:   "macros [name]",
	Short: "Run predefined macros from honeyfile manifest",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runMacros,
}

var (
	flagMacrosInitFile  string
	flagMacrosInitForce bool
)

const macrosInitTemplate = `apiVersion: honey.shareed2k.io/v1alpha1
kind: MacroSet
metadata:
  name: my-macros
spec:
  macros:
    # Run a shell command across matching hosts
    restart-nginx:
      kind: exec
      target: "web-*"
      command: "systemctl restart nginx"
      parallel: 20
      timeout: 30s
      shell: auto

    # Run a multi-step command sequence
    check-disk:
      kind: exec
      target: "*"
      commands:
        - "df -h /"
        - "du -sh /var/log"
      parallel: 50
      timeout: 15s
      output: text

    # Run a CUE recipe (dry-run by default)
    deploy:
      kind: recipe
      target: "web-*"
      recipePath: "deploy.cue"
      execute: false
      env:
        - "APP_ENV=staging"

    # Tail logs across hosts
    tail-errors:
      kind: logs
      target: "web-*"
      unit: "nginx"
      follow: true
      tail: 200
      grep: "error"

    # Open an HTTP application proxy
    open-admin:
      kind: app
      app: "admin-ui"
      openBrowser: true

    # Start a TCP tunnel
    postgres-tunnel:
      kind: tunnel
      app: "postgres-tcp"
`

var macrosInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Scaffold a honeyfile.yaml with example macros",
	RunE: func(cmd *cobra.Command, _ []string) error {
		target := flagMacrosInitFile
		if target == "" {
			target = "honeyfile.yaml"
		}
		if !flagMacrosInitForce {
			if _, err := os.Stat(target); err == nil {
				return fmt.Errorf("%s already exists; use --force to overwrite", target)
			}
		}
		if err := os.WriteFile(target, []byte(macrosInitTemplate), 0o600); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "created %s\n", target)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(macrosCmd)
	macrosCmd.AddCommand(macrosInitCmd)
	macrosCmd.Flags().StringVar(&flagMacrosFile, "file", "", "Path to honeyfile manifest")
	macrosCmd.Flags().BoolVar(&flagMacrosList, "list", false, "List available macros")
	macrosCmd.Flags().BoolVar(&flagMacrosDryRun, "dry-run", false, "Print resolved macro and exit")
	macrosCmd.Flags().StringVarP(&flagMacrosOutput, "output", "o", "text", "Output format: text or json")
	macrosInitCmd.Flags().StringVar(&flagMacrosInitFile, "file", "", "Output path (default: honeyfile.yaml)")
	macrosInitCmd.Flags().BoolVar(&flagMacrosInitForce, "force", false, "Overwrite existing file")
}

func runMacros(cmd *cobra.Command, args []string) error {
	if flagMacrosOutput != "text" && flagMacrosOutput != "json" {
		return fmt.Errorf("--output must be one of: text, json")
	}
	path, err := macros.ResolvePath(flagMacrosFile)
	if err != nil {
		return err
	}
	set, err := macros.Load(path)
	if err != nil {
		return err
	}
	if flagMacrosList {
		return printMacrosList(set)
	}
	if len(args) != 1 {
		return fmt.Errorf("macro name required (or use --list)")
	}
	name := args[0]
	m, ok := set.Spec.Macros[name]
	if !ok {
		return fmt.Errorf("macro %q not found", name)
	}
	if flagMacrosDryRun {
		return printDryRun(name, m)
	}
	applyMacroSearchFlags(m)
	switch m.Kind {
	case "exec":
		return runExecMacro(cmd, m)
	case "recipe":
		return runRecipeMacro(cmd, m)
	case "logs":
		return runLogsMacro(cmd, m)
	case "app":
		return runAppMacro(cmd, m)
	case "tunnel":
		return runTunnelMacro(cmd, m)
	default:
		return fmt.Errorf("unsupported macro kind %q", m.Kind)
	}
}

func printMacrosList(set *macros.MacroSet) error {
	if flagMacrosOutput == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(set.Spec.Macros)
	}
	for name, m := range set.Spec.Macros {
		target := m.Target
		if target == "" {
			target = m.App
		}
		fmt.Printf("%s\t%s\t%s\n", name, m.Kind, target)
	}
	return nil
}

func printDryRun(name string, m macros.Macro) error {
	if flagMacrosOutput == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"name": name, "macro": m})
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	fmt.Printf("macro %s:\n%s\n", name, string(b))
	return nil
}

func applyMacroSearchFlags(m macros.Macro) {
	flagName = strings.TrimSpace(m.Name)
	flagNameRegex = strings.TrimSpace(m.NameRegex)
	flagProviders = strings.TrimSpace(m.Provider)
	flagBackends = strings.TrimSpace(m.Backends)
}

func runExecMacro(cmd *cobra.Command, m macros.Macro) error {
	flagExecParallel = 20
	if m.Parallel > 0 {
		flagExecParallel = m.Parallel
	}
	flagExecRetry = 1
	if m.Retry > 0 {
		flagExecRetry = m.Retry
	}
	flagExecTimeout = 0
	if strings.TrimSpace(m.Timeout) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(m.Timeout))
		if err != nil {
			return err
		}
		flagExecTimeout = d
	}
	flagExecRunAs = strings.TrimSpace(m.RunAs)
	flagExecShell = strings.TrimSpace(m.Shell)
	if flagExecShell == "" {
		flagExecShell = "auto"
	}
	flagExecOutput = m.Output
	if flagExecOutput == "" {
		flagExecOutput = "text"
	}
	if m.Quiet != nil {
		flagExecQuiet = *m.Quiet
	}
	command := strings.TrimSpace(m.Command)
	if len(m.Commands) > 0 {
		command = strings.Join(m.Commands, " && ")
	}
	return runExec(cmd, []string{m.Target, command})
}

func runRecipeMacro(cmd *cobra.Command, m macros.Macro) error {
	flagCueExecExecute = false
	if m.Execute != nil {
		flagCueExecExecute = *m.Execute
	}
	flagCueExecEnv = append([]string(nil), m.Env...)
	return runCueExec(cmd, []string{m.RecipePath, m.Target})
}

func runLogsMacro(cmd *cobra.Command, m macros.Macro) error {
	flagLogsFollow = m.Follow != nil && *m.Follow
	flagLogsUnit = m.Unit
	flagLogsFile = m.File
	flagLogsCommand = m.Cmd
	flagLogsRunAs = m.RunAs
	if m.Tail != nil {
		flagLogsTail = *m.Tail
	} else {
		flagLogsTail = 100
	}
	if strings.TrimSpace(m.Since) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(m.Since))
		if err != nil {
			return err
		}
		flagLogsSince = d
	} else {
		flagLogsSince = 0
	}
	flagLogsGrep = m.Grep
	flagLogsLabels = append([]string(nil), m.Labels...)
	flagLogsOutputFile = m.OutputFile
	flagLogsTUI = m.TUI != nil && *m.TUI
	if m.MaxConcurrency != nil {
		flagLogsMaxConcurrency = *m.MaxConcurrency
	}
	if m.Timestamps != nil {
		flagLogsTimestamps = *m.Timestamps
	}
	args := []string{m.Target}
	if s := strings.TrimSpace(m.Source); s != "" {
		args = append(args, s)
	}
	return runLogs(cmd, args)
}

func runAppMacro(cmd *cobra.Command, m macros.Macro) error {
	flagAppBrowser = true
	flagAppNoBrowser = false
	if m.OpenBrowser != nil && !*m.OpenBrowser {
		flagAppBrowser = false
		flagAppNoBrowser = true
	}
	return runProxyApp(cmd, m.App, apps.AppTypeHTTP)
}

func runTunnelMacro(cmd *cobra.Command, m macros.Macro) error {
	// Detached/background is handled by proxy manager internals in runProxyApp.
	return runProxyApp(cmd, m.App, apps.AppTypeTCP)
}
