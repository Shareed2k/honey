package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/provider/truenasprovider"
	"github.com/shareed2k/honey/internal/truenasshell"
	"github.com/shareed2k/honey/internal/webserver"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var ptyProxyConfig string

var ptyProxyCmd = &cobra.Command{
	Use:    "pty-proxy <base64_payload>",
	Short:  "Internal sub-command to proxy terminal sessions",
	Hidden: true, // Do not show in the CLI help
	Args:   cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		zap.L().Debug("honey pty-proxy invoked")

		if err := loadHostexecFromHoneyConfig(ptyProxyConfig); err != nil {
			ptyProxyPauseOnError(err)
			return nil
		}

		payloadBytes, err := base64.StdEncoding.DecodeString(args[0])
		if err != nil {
			zap.L().Error("pty-proxy: decode payload failed", zap.Error(err))
			fmt.Printf("\r\n\033[31m[honey] Error decoding payload: %v\033[0m\r\n", err)
			return nil
		}

		var hello webserver.WSHello
		if err := json.Unmarshal(payloadBytes, &hello); err != nil {
			zap.L().Error("pty-proxy: unmarshal payload failed", zap.Error(err))
			fmt.Printf("\r\n\033[31m[honey] Error unmarshaling payload: %v\033[0m\r\n", err)
			return nil
		}

		zap.L().Debug("honey pty-proxy starting",
			zap.String("session", hello.SessionID),
			zap.String("host", hello.Record.Name),
			zap.String("console", hello.Console),
		)

		if truenasshell.ShouldUseTrueNASShell(hello.Record, hello.Console) {
			if _, ok := truenasprovider.BackendByName(hello.Record.Meta["backend_name"]); !ok {
				ptyProxyPauseOnError(fmt.Errorf("TrueNAS API shell: backend %q not found in config (check --config / HONEY_CONFIG)",
					hello.Record.Meta["backend_name"]))
				return nil
			}
		}

		// Run Terminal Interactive handles pure SSH, Proxmox Serial, and Kubernetes pods natively
		// using os.Stdin/Stdout/Stderr and registers for SIGWINCH to handle resizes forwarded by tmux/zellij!
		err = engine.RunTerminalInteractive(hello.SSHUser, hello.Record, hello.Console, buildHostExecRegistry())
		if err != nil {
			ptyProxyPauseOnError(err)
		}

		return nil
	},
}

func init() {
	ptyProxyCmd.Flags().StringVar(&ptyProxyConfig, "config", "", "Path to honey YAML (optional; also HONEY_CONFIG or default paths)")
	rootCmd.AddCommand(ptyProxyCmd)
}

func ptyProxyPauseOnError(err error) {
	zap.L().Error("pty-proxy: connection error", zap.Error(err))
	fmt.Printf("\r\n\033[31m[honey] Connection Error: %v\033[0m\r\n", err)
	// Pause before returning so tmux keeps the pane open long enough for the browser to read the PTY.
	fmt.Printf("\r\n[honey] Press ENTER to close this terminal...")
	var b [1]byte
	_, _ = os.Stdin.Read(b[:])
}

// loadHostexecFromHoneyConfig loads backend credentials into hostexec for pty-proxy subprocesses.
func loadHostexecFromHoneyConfig(explicit string) error {
	cfgPath, err := config.ResolvePath(explicit)
	if err != nil {
		return err
	}
	if cfgPath == "" {
		getSearchRegistry().ReconfigureFromConfig()
		return nil
	}
	if _, err := config.Load(cfgPath); err != nil {
		return err
	}
	getSearchRegistry().ReconfigureFromConfig()
	return nil
}
