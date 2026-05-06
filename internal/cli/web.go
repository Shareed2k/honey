package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/webserver"
)

var (
	webListen string
	webConfig string
)

var webCmd = &cobra.Command{
	Use:   "web",
	Short: "Start embedded web UI (loopback + token) for backends, search, config, SSH terminal, and uploads",
	Args:  cobra.NoArgs,
	RunE:  runWeb,
}

func init() {
	webCmd.Flags().StringVar(&webListen, "listen", "127.0.0.1:8765", "Listen address (host:port); must be loopback for safe default")
	webCmd.Flags().StringVar(&webConfig, "config", "", "Path to honey YAML (optional; same as honey search)")
	rootCmd.AddCommand(webCmd)
}

func runWeb(_ *cobra.Command, _ []string) error {
	if err := assertLoopback(webListen); err != nil {
		return err
	}
	token, err := webserver.AuthToken()
	if err != nil {
		return err
	}
	srv, err := webserver.NewServer(webserver.Options{
		ListenAddr: webListen,
		Token:      token,
		ConfigPath: webConfig,
		Version:    BuildVersion(),
		Commit:     BuildCommit(),
		Date:       BuildDate(),
	})
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(os.Stderr, "\nHoney Web UI (Ctrl+C to stop)\n  URL:   http://%s/?token=%s\n  API:   Authorization: Bearer <token>  or  X-Honey-Token: <token>\n  WS:    /ws/ssh?token=<token>\n\n", webListen, token)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return srv.Start(ctx)
}

func assertLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return fmt.Errorf("refusing to bind %q: only 127.0.0.1, localhost, or ::1 allowed (override safety in a future release)", host)
	}
	return nil
}
