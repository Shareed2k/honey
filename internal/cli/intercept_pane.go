//go:build !windows

package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"golang.org/x/term"
	"k8s.io/client-go/kubernetes"

	"github.com/shareed2k/mogate/pkg/local"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/intercept"
	"github.com/shareed2k/honey/internal/interceptwire"
	"github.com/shareed2k/honey/internal/webserver"
)

// interceptPaneConfig is the --config path for the intercept-pane subprocess.
// Like pty-proxy's own --config, it shadows the root persistent flag so tmux can
// launch the pane with an explicit config path; empty falls back to HONEY_CONFIG
// and the default search paths.
var interceptPaneConfig string

// interceptPaneCmd IS the tmux pane that runs a browser-driven interception: the
// web server launches `honey intercept-pane <base64>` inside a pane, and this
// command decodes the (secret-free) payload, rebuilds the interception from its
// own --config, and runs the injected shell on the pane's real PTY (os.Std*).
// A later task points /ws/intercept at this command via tmux. It mirrors
// pty-proxy: Hidden, one base64 arg, and it prints errors in red into the pane
// instead of crashing so the operator can read them.
var interceptPaneCmd = &cobra.Command{
	Use:    "intercept-pane <base64_payload>",
	Short:  "Internal sub-command that runs one web interception inside a tmux pane",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		zap.L().Debug("honey intercept-pane invoked")
		// The pane owns its own lifetime (tmux, not the parent request), so it
		// runs under a fresh signal-cancelled context, not cmd.Context().
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		defer stop()

		if err := runInterceptPane(ctx, args[0]); err != nil {
			interceptPanePauseOnError(err)
		}
		return nil
	},
}

func init() {
	interceptPaneCmd.Flags().StringVar(&interceptPaneConfig, "config", "", "Path to honey YAML (optional; also HONEY_CONFIG or default paths)")
	rootCmd.AddCommand(interceptPaneCmd)
}

// runInterceptPane decodes the payload, resolves the operator config from the
// pane's --config, wires the real cluster dependencies (reusing the exact graph
// runIntercept builds), and runs one interception session on the pane's PTY.
// Every failure is returned wrapped for the caller to print into the pane.
func runInterceptPane(ctx context.Context, encoded string) error {
	req, err := decodeInterceptPaneRequest(encoded)
	if err != nil {
		return err
	}
	// Mirrors buildInterceptOptions' guard on the fallback path: the mapper
	// itself doesn't check this, so the pane must.
	if len(req.EnvInclude) > 0 && len(req.EnvExclude) > 0 {
		return errors.New("intercept-pane: env_include and env_exclude are mutually exclusive")
	}

	cfg, err := loadPaneConfig(interceptPaneConfig)
	if err != nil {
		return fmt.Errorf("intercept-pane: load config: %w", err)
	}
	if cfg == nil || cfg.Intercept == nil || !cfg.Intercept.Enabled {
		return errors.New("intercept-pane: intercept is not configured; set intercept.enabled and intercept.agent_image in the honey config")
	}

	restCfg, err := interceptWebRestConfig(cfg, req.Record)
	if err != nil {
		return err
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return fmt.Errorf("intercept-pane: build kubernetes client: %w", err)
	}

	// The shared mapper validates the pod record and applies the operator's
	// agent image; the web handler uses the exact same call, so both paths stay
	// in lockstep.
	opts, err := intercept.OptionsFromPodRecord(req.Record, req.Modes, req.UDP, req.Command, cfg.Intercept.AgentImage)
	if err != nil {
		return err
	}
	// The mapper omits these; the web handler's buildInterceptOptions sets
	// them the same way for the fallback path (including the Container trim,
	// so whitespace behaves identically on both paths).
	opts.Container = strings.TrimSpace(req.Container)
	opts.EnvInclude = req.EnvInclude
	opts.EnvExclude = req.EnvExclude
	opts.Actor = req.Actor

	deps, sink, err := interceptwire.BuildDeps(ctx, cfg, restCfg, clientset, opts.Namespace, opts.Pod, "")
	if err != nil {
		return err
	}
	defer func() { _ = sink.Close() }()

	// The pane's stdio IS a real terminal (tmux gave the pane a PTY), so run the
	// child on os.Std* under a pseudo-terminal, fed by a SIGWINCH resize channel.
	deps.LocalRunner = paneLocalRunner{
		inner:  deps.LocalRunner,
		resize: paneResizeChan(ctx, int(os.Stdin.Fd()), req.Cols, req.Rows),
	}

	if err := intercept.New(deps, opts).Run(ctx); err != nil {
		if errors.Is(err, intercept.ErrNoInjector) {
			return fmt.Errorf("%w\nno interception injector is bundled for this platform", err)
		}
		return err
	}
	return nil
}

// decodeInterceptPaneRequest decodes the base64(JSON) argv payload into an
// InterceptPaneRequest. It never touches secrets — the payload carries none.
func decodeInterceptPaneRequest(encoded string) (webserver.InterceptPaneRequest, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return webserver.InterceptPaneRequest{}, fmt.Errorf("intercept-pane: decode payload: %w", err)
	}
	var req webserver.InterceptPaneRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return webserver.InterceptPaneRequest{}, fmt.Errorf("intercept-pane: unmarshal payload: %w", err)
	}
	return req, nil
}

// loadPaneConfig loads the operator config for the intercept-pane subprocess,
// respecting --config, HONEY_CONFIG, and the default search paths — the same
// resolution the root command performs for runIntercept's cfg. A resolved-but-
// empty path (no config found) returns a nil *config.File, which the caller
// rejects with a clear "intercept is not configured" error.
func loadPaneConfig(explicit string) (*config.File, error) {
	cfgPath, err := config.ResolvePath(explicit)
	if err != nil {
		return nil, err
	}
	if cfgPath == "" {
		return nil, nil
	}
	return config.Load(cfgPath)
}

// paneLocalRunner is the intercept.LocalRunner for the tmux-pane path: it forces
// a pseudo-terminal on the pane's own os.Std* streams (tmux owns the pane's
// terminal) and wires a SIGWINCH-driven resize channel, then delegates to the
// wrapped production runner. It never logs cfg, which carries the relay socket
// path and token-file location.
type paneLocalRunner struct {
	inner  intercept.LocalRunner
	resize <-chan local.Winsize
}

// Run augments cfg with the PTY + pane streams and delegates to the inner runner.
func (r paneLocalRunner) Run(ctx context.Context, cfg local.Config, command []string) error {
	cfg.Pty = true
	cfg.Stdin = os.Stdin
	cfg.Stdout = os.Stdout
	cfg.Stderr = os.Stdout
	cfg.ResizeCh = r.resize
	return r.inner.Run(ctx, cfg, command)
}

var _ intercept.LocalRunner = paneLocalRunner{}

// paneResizeChan starts a SIGWINCH-driven resize feeder for the tmux pane. It
// seeds the returned channel with the initial cols/rows, then reads the pane's
// current terminal size on every SIGWINCH and forwards it. The goroutine is
// ctx-tied: it exits — and closes the channel, which ends mogate's resize pump —
// when ctx is cancelled. The channel is buffered so a resize burst never blocks
// the signal goroutine, and every send also selects on ctx.Done so a stalled
// consumer can never wedge it past cancellation.
func paneResizeChan(ctx context.Context, fd, cols, rows int) <-chan local.Winsize {
	ch := make(chan local.Winsize, 8)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGWINCH)
	go func() {
		defer close(ch)
		defer signal.Stop(sig)

		send := func(w local.Winsize) bool {
			select {
			case ch <- w:
				return true
			case <-ctx.Done():
				return false
			}
		}

		if !send(paneWinsize(cols, rows)) {
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-sig:
				w, h, err := term.GetSize(fd)
				if err != nil {
					continue
				}
				if !send(paneWinsize(w, h)) {
					return
				}
			}
		}
	}()
	return ch
}

// paneWinsize converts a cols/rows pair to a local.Winsize, applying the same
// terminal defaults as the SSH path and clamping to the uint16 range so the
// conversion is always in bounds — a real guard, not a gosec suppression.
func paneWinsize(cols, rows int) local.Winsize {
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 32
	}
	if cols > math.MaxUint16 {
		cols = math.MaxUint16
	}
	if rows > math.MaxUint16 {
		rows = math.MaxUint16
	}
	return local.Winsize{Cols: uint16(cols), Rows: uint16(rows)}
}

// interceptPanePauseOnError prints err in red into the pane and pauses so tmux
// keeps the pane open long enough for the operator to read it, then returns so
// the command exits cleanly (the pane is torn down by the parent, not a crash).
// It mirrors pty-proxy's ptyProxyPauseOnError with an intercept-accurate label.
func interceptPanePauseOnError(err error) {
	zap.L().Error("intercept-pane: session error", zap.Error(err))
	fmt.Printf("\r\n\033[31m[honey] Interception error: %v\033[0m\r\n", err)
	fmt.Printf("\r\n[honey] Press ENTER to close this terminal...")
	var b [1]byte
	_, _ = os.Stdin.Read(b[:])
}
