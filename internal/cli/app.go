package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/apps"
	"github.com/shareed2k/honey/internal/appsecret"
	"github.com/shareed2k/honey/internal/hostapi"
	"github.com/shareed2k/honey/internal/proxy"
	"github.com/shareed2k/honey/internal/ui"
)

var (
	flagAppPort     int
	flagAppBrowser  bool
	flagAppPrintURL bool
)

var appCmd = &cobra.Command{
	Use:   "app",
	Short: "Manage and connect to application proxies",
}

var appListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured applications",
	RunE: func(_ *cobra.Command, _ []string) error {
		cfg := resolvedCfg
		if cfg == nil || len(cfg.Apps) == 0 {
			fmt.Println("No apps configured.")
			return nil
		}

		fmt.Printf("%-20s %-8s %-20s %-30s %s\n", "NAME", "TYPE", "TARGET", "UPSTREAM", "LOCAL PORT")
		for name, app := range cfg.Apps {
			fmt.Printf("%-20s %-8s %-20s %-30s %d\n", name, app.Type, app.Target, app.Upstream, app.LocalPort)
		}
		return nil
	},
}

var appOpenCmd = &cobra.Command{
	Use:   "open <name>",
	Short: "Open an HTTP application proxy",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		return runProxyApp(cmd, name, apps.AppTypeHTTP)
	},
}

func init() {
	rootCmd.AddCommand(appCmd)
	appCmd.AddCommand(appListCmd)
	appCmd.AddCommand(appOpenCmd)

	appOpenCmd.Flags().IntVar(&flagAppPort, "port", 0, "Override local port")
	appOpenCmd.Flags().BoolVar(&flagAppBrowser, "browser", false, "Open browser automatically")
	appOpenCmd.Flags().BoolVar(&flagAppPrintURL, "print-url", false, "Print only the URL (useful for scripting)")
	appOpenCmd.Flags().StringVar(&flagProviders, "provider", "", "Comma-separated: gcp,aws,k8s,consul,proxmox,truenas,docker,local (default: all)")
	appOpenCmd.Flags().StringVar(&flagBackends, "backends", "", "Comma-separated backend names (YAML backends.*.name); only those entries run")
	appOpenCmd.Flags().StringVar(&flagSSHUser, "ssh-user", "", "Default SSH user for connect actions (defaults to config or OS user)")
	getSearchRegistry().RegisterAllProviderFlags(appOpenCmd)
}

func resolveAppTarget(ctx context.Context, app apps.AppConfig, cfgPath string, cache *ui.ClientCache) (proxy.Dialer, io.Closer, error) {
	searchBackends := flagBackends
	if app.Backend != "" {
		searchBackends = app.Backend
	}

	searchProviders := flagProviders
	if app.Provider != "" {
		searchProviders = app.Provider
	}

	if !flagAppPrintURL {
		if app.TargetRegex != "" {
			fmt.Printf("Resolving app target via regex: %s\n", app.TargetRegex)
		} else {
			fmt.Printf("Resolving app target: %s\n", app.Target)
		}
	}
	in := hostapi.SearchHostsInput{
		Name:       app.Target,
		NameRegex:  app.TargetRegex,
		ConfigPath: cfgPath,
		SSHUser:    flagSSHUser,
		Providers:  searchProviders,
		Backends:   searchBackends,
	}
	out, err := hostapi.SearchHosts(ctx, &in, buildHostExecRegistry(), getSearchRegistry())
	if err != nil {
		if app.TargetRegex != "" {
			return nil, nil, fmt.Errorf("resolve target (regex %q): %w", app.TargetRegex, err)
		}
		return nil, nil, fmt.Errorf("resolve target %q: %w", app.Target, err)
	}
	if len(out.Records) == 0 {
		if app.TargetRegex != "" {
			return nil, nil, fmt.Errorf("target regex %q not found", app.TargetRegex)
		}
		return nil, nil, fmt.Errorf("target %q not found", app.Target)
	}

	rec := out.Records[0]
	if !flagAppPrintURL {
		if ui.TransportForAppDialer(rec) == ui.AppDialerTransportSSH {
			fmt.Printf("Opening SSH tunnel via %s\n", rec.Name)
		} else {
			fmt.Printf("Opening in-memory tunnel via %s\n", rec.Name)
		}
	}

	return ui.ResolveAppDialerWithCache(flagSSHUser, rec, cache)
}

func runProxyApp(cmd *cobra.Command, name string, forceType apps.AppType) error {
	cfgPath := resolvedCfgPath
	cfg := resolvedCfg
	if cfg == nil {
		return fmt.Errorf("no config file found; run 'honey config' to create one")
	}

	app, ok := cfg.Apps[name]
	if !ok {
		return fmt.Errorf("app %q not found in config", name)
	}

	if forceType != "" && app.Type != forceType {
		return fmt.Errorf("app %q is type %s, expected %s", name, app.Type, forceType)
	}
	resolvedUpstream, err := appsecret.ResolveUpstream(cmd.Context(), cfg, app.Upstream)
	if err != nil {
		return err
	}
	app.Upstream = resolvedUpstream

	if flagAppPort > 0 {
		app.LocalPort = flagAppPort
	}

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()
	appProxyCache := ui.NewClientCache()
	reg := buildHostExecRegistry()
	reg.Reconfigure(cfg)
	appProxyCache.SetRegistry(reg)
	defer appProxyCache.CloseAll()

	mgr := proxy.NewManager(proxy.NewLogger(zap.L()))

	var dialer proxy.Dialer
	var closer io.Closer
	if app.Target == "" && app.TargetRegex == "" {
		dialer = proxy.DirectDialer{}
	} else {
		var err error
		dialer, closer, err = resolveAppTarget(ctx, app, cfgPath, appProxyCache)
		if err != nil {
			return err
		}
	}

	if !flagAppPrintURL {
		fmt.Println("Starting local proxy...")
	}
	_, err = mgr.Start(ctx, app, dialer, closer)
	if err != nil {
		return err
	}

	localURL := fmt.Sprintf("http://127.0.0.1:%d", app.LocalPort)
	if app.Type == apps.AppTypeTCP {
		localURL = fmt.Sprintf("127.0.0.1:%d", app.LocalPort)
	}

	if flagAppPrintURL {
		fmt.Println(localURL)
	} else {
		fmt.Printf("App available at: %s\n", localURL)
	}

	if app.Type == apps.AppTypeHTTP {
		shouldOpen := app.OpenBrowser
		if flagAppBrowser {
			shouldOpen = true
		}
		if shouldOpen && !flagAppPrintURL {
			_ = openBrowser(localURL)
		}
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case <-sigCh:
		if !flagAppPrintURL {
			fmt.Println("\nShutting down proxy...")
		}
	case <-ctx.Done():
	}

	return nil
}

func openBrowser(url string) error {
	// #nosec G204 -- The URL is generated internally by the application (either 127.0.0.1 or the local web server URL), not from untrusted input
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return fmt.Errorf("unsupported platform")
	}
}
