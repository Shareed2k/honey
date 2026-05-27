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

func init() {
	rootCmd.AddCommand(macrosCmd)
	macrosCmd.Flags().StringVar(&flagMacrosFile, "file", "", "Path to honeyfile manifest")
	macrosCmd.Flags().BoolVar(&flagMacrosList, "list", false, "List available macros")
	macrosCmd.Flags().BoolVar(&flagMacrosDryRun, "dry-run", false, "Print resolved macro and exit")
	macrosCmd.Flags().StringVarP(&flagMacrosOutput, "output", "o", "text", "Output format: text or json")
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
