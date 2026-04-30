package cli

import (
	"context"
	"log"
	"os"

	"github.com/spf13/cobra"

	"hostctl/internal/mcpserver"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Run the Model Context Protocol (stdio) server",
	Long: `Starts the MCP server on stdin/stdout for Cursor, Claude Desktop, and other MCP clients.

Only stderr may be used for logging; stdout carries the JSON-RPC stream.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		_ = cmd
		log.SetOutput(os.Stderr)
		log.SetPrefix("hostctl mcp: ")
		return mcpserver.Run(context.Background())
	},
}

func init() {
	rootCmd.AddCommand(mcpCmd)
}
