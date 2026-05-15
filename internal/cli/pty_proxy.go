package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/ui"
	"github.com/shareed2k/honey/internal/webserver"
)

var ptyProxyCmd = &cobra.Command{
	Use:    "pty-proxy <base64_payload>",
	Short:  "Internal sub-command to proxy terminal sessions",
	Hidden: true, // Do not show in the CLI help
	Args:   cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		zap.L().Debug("honey pty-proxy invoked")

		payloadBytes, err := base64.StdEncoding.DecodeString(args[0])
		if err != nil {
			fmt.Printf("\r\n\033[31m[honey] Error decoding payload: %v\033[0m\r\n", err)
			return nil
		}

		var hello webserver.WSHello
		if err := json.Unmarshal(payloadBytes, &hello); err != nil {
			fmt.Printf("\r\n\033[31m[honey] Error unmarshaling payload: %v\033[0m\r\n", err)
			return nil
		}

		zap.L().Debug("honey pty-proxy starting", zap.String("session", hello.SessionID), zap.String("host", hello.Record.Name))

		// Run Terminal Interactive handles pure SSH, Proxmox Serial, and Kubernetes pods natively
		// using os.Stdin/Stdout/Stderr and registers for SIGWINCH to handle resizes forwarded by tmux/zellij!
		err = ui.RunTerminalInteractive(hello.SSHUser, hello.Record)
		if err != nil {
			fmt.Printf("\r\n\033[31m[honey] Connection Error: %v\033[0m\r\n", err)

			// We MUST pause before returning! If we return instantly, tmux will destroy the window
			// before the browser has a chance to read the error message off the PTY pipe!
			fmt.Printf("\r\n[honey] Press ENTER to close this terminal...")
			var b [1]byte
			_, _ = os.Stdin.Read(b[:])
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(ptyProxyCmd)
}
