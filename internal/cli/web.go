package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/metrics"
	"github.com/shareed2k/honey/internal/webserver"
)

var (
	webListen             string
	webConfig             string
	webFilesRoot          string
	webAgentBin           string
	webAgentBuildCacheDir string
	webMetricsListen      string
)

var webCmd = &cobra.Command{
	Use:   "web",
	Short: "Start embedded web UI (loopback + token) for backends, search, config, SSH terminal, and uploads",
	Args:  cobra.NoArgs,
	RunE:  runWeb,
}

func init() {
	webCmd.Flags().StringVar(&webListen, "listen", "localhost:8765", "Listen address (host:port); must be loopback for safe default")
	webCmd.Flags().StringVar(&webConfig, "config", "", "Path to honey YAML (optional; same as honey search)")
	webCmd.Flags().StringVar(&webFilesRoot, "files-root", "", "Local filesystem root for the web file browser (default: $HONEY_FILES_ROOT or $HOME)")
	webCmd.Flags().StringVar(&webAgentBin, "agent-bin", "", "Explicit path to honey-transfer-agent binary (optional)")
	webCmd.Flags().StringVar(&webAgentBuildCacheDir, "agent-build-cache-dir", "", "Directory used to cache auto-built honey-transfer-agent binary")
	webCmd.Flags().StringVar(&webMetricsListen, "metrics-listen", "", "Optional loopback host:port for Prometheus /metrics (e.g. 127.0.0.1:9091)")
	rootCmd.AddCommand(webCmd)
}

func runWeb(cmd *cobra.Command, _ []string) error {
	if err := assertLoopback(webListen); err != nil {
		return err
	}
	if strings.TrimSpace(webMetricsListen) != "" {
		if err := assertLoopback(webMetricsListen); err != nil {
			return err
		}
	}
	token, err := webserver.AuthToken()
	if err != nil {
		return err
	}
	cfgPath, err := config.ResolvePath(webConfig)
	if err != nil {
		return err
	}
	var cfg *config.File
	if cfgPath != "" {
		cfg, err = config.Load(cfgPath)
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}
	}
	recordDir := config.ResolveRecordDir(cfg, cfgPath, flagRecordDir, recordDirFlagChanged(cmd))
	var prom *metrics.Registry
	if strings.TrimSpace(webMetricsListen) != "" {
		prom = metrics.NewRegistry(BuildVersion(), BuildCommit())
	}
	srv, err := webserver.NewServer(webserver.Options{
		ListenAddr:         webListen,
		Token:              token,
		ConfigPath:         cfgPath,
		Config:             cfg,
		RecordDir:          recordDir,
		LocalFilesRoot:     webFilesRoot,
		AgentBinaryPath:    webAgentBin,
		AgentBuildCacheDir: webAgentBuildCacheDir,
		Version:            BuildVersion(),
		Commit:             BuildCommit(),
		Date:               BuildDate(),
		MetricsListenAddr:  strings.TrimSpace(webMetricsListen),
		Metrics:            prom,
		NoCache:            flagNoCache,
		Refresh:            flagRefresh,
	})
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(os.Stderr, "\nHoney Web UI (Ctrl+C to stop)\n  URL:   http://%s/?token=%s\n  API:   Authorization: Bearer <token>  or  X-Honey-Token: <token>\n  WS:    /ws/ssh?token=<token>\n  Assist: OPENAI_API_KEY (+ optional OPENAI_BASE_URL)\n", webListen, token)
	if strings.TrimSpace(webMetricsListen) != "" {
		_, _ = fmt.Fprintf(os.Stderr, "  Metrics: http://%s/metrics\n", webMetricsListen)
	}
	_, _ = fmt.Fprintln(os.Stderr)

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
