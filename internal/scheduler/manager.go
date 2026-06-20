// Package scheduler provides a cron-based trigger layer for CUE recipe apps.
// It scans the honey config for apps whose target recipe has one or more
// RecipeSchedule entries and registers a gronx task for each one.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
	"time"

	"github.com/adhocore/gronx/pkg/tasker"
	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/apps"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hostapi"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/metrics"
	"github.com/shareed2k/honey/internal/plugins"
	"github.com/shareed2k/honey/internal/postgres"
	"github.com/shareed2k/honey/internal/queue"
	"github.com/shareed2k/honey/internal/safepath"
	"github.com/shareed2k/honey/internal/searchrun"
	"github.com/shareed2k/honey/internal/ui"
)

// ScheduleEntry is a resolved schedule derived from one app's recipe.
type ScheduleEntry struct {
	AppName      string
	ScheduleName string
	RecipePath   string
	Schedule     cuetry.RecipeSchedule
	App          apps.AppConfig
}

// Options configures the Manager.
type Options struct {
	ConfigPath     string
	Config         *config.File
	ExecRegistry   hostexec.Registry
	SearchRegistry *searchrun.Registry
	RecordDir      string
	Queue          queue.Queue
	Metrics        *metrics.Registry
	Pools          *postgres.PoolManager
	Cache          *engine.ClientCache // optional shared SSH client cache; nil = per-run cache
}

// Manager builds and runs all cron schedules derived from recipe apps.
type Manager struct {
	opts    Options
	taskers []*tasker.Tasker // one per unique timezone
	count   int
	entries []ScheduleEntry
}

// New creates a Manager and registers all eligible recipe schedule tasks.
// It returns an error if no config is provided or no tasks could be registered.
func New(opts Options) (*Manager, error) {
	if opts.Config == nil {
		return nil, fmt.Errorf("scheduler: config is required")
	}

	m := &Manager{opts: opts}
	m.register()
	return m, nil
}

// Run blocks and runs all cron schedulers until ctx is cancelled.
// All but the last tasker are run in background goroutines; the last one blocks.
func (m *Manager) Run(ctx context.Context) {
	for i, t := range m.taskers {
		if i < len(m.taskers)-1 {
			go t.WithContext(ctx).Run()
		} else {
			t.WithContext(ctx).Run() // last one blocks
		}
	}
}

// Entries returns all registered schedule entries (used by the list API).
func (m *Manager) Entries() []ScheduleEntry {
	return m.entries
}

// Start begins all cron schedulers in background goroutines and returns immediately.
// The schedulers stop when ctx is cancelled.
func (m *Manager) Start(ctx context.Context) {
	for _, t := range m.taskers {
		go t.WithContext(ctx).Run()
	}
}

// TaskCount returns the number of cron tasks that were registered.
func (m *Manager) TaskCount() int {
	return m.count
}

// register iterates all apps, parses recipe schedules, and registers gronx tasks.
// One tasker.Tasker is created per unique timezone; they are stored in m.taskers.
// If m.opts.Config is nil, register is a no-op (used by LoadScheduleEntries).
func (m *Manager) register() {
	if m.opts.Config == nil {
		return
	}

	tzTaskers := map[string]*tasker.Tasker{}

	for appName, app := range m.opts.Config.Apps {
		if app.Type != apps.AppTypeRecipe || strings.TrimSpace(app.TargetRecipe) == "" {
			continue
		}

		recipePath := strings.TrimSpace(app.TargetRecipe)
		if !filepath.IsAbs(recipePath) && m.opts.ConfigPath != "" {
			recipePath = filepath.Join(filepath.Dir(m.opts.ConfigPath), recipePath)
		}

		raw, err := safepath.ReadFile(recipePath)
		if err != nil {
			zap.L().Warn("scheduler: skipping app — cannot read recipe",
				zap.String("app", appName),
				zap.String("path", recipePath),
				zap.Error(err),
			)
			continue
		}

		recipe, err := cuetry.ParseRemoteRecipeOpts(raw, nil, cuetry.ParseOptions{})
		if err != nil {
			zap.L().Warn("scheduler: skipping app — cannot parse recipe",
				zap.String("app", appName),
				zap.Error(err),
			)
			continue
		}

		for schedName, sched := range recipe.Schedules {
			if strings.TrimSpace(sched.Cron) == "" {
				zap.L().Warn("scheduler: skipping schedule — empty cron expression",
					zap.String("app", appName),
					zap.String("schedule", schedName),
				)
				continue
			}

			// Capture loop variables for the closure.
			capturedApp := app
			capturedAppName := appName
			capturedSchedule := sched
			capturedScheduleName := schedName
			capturedRecipePath := recipePath
			capturedRecipe := recipe

			// Look up or create the tasker for this schedule's timezone.
			tz := strings.TrimSpace(capturedSchedule.TimeZone)
			if tz == "" {
				tz = "UTC"
			}
			if _, ok := tzTaskers[tz]; !ok {
				if _, err := time.LoadLocation(tz); err != nil {
					zap.L().Warn("scheduler: invalid timezone, skipping these schedules",
						zap.String("timezone", tz),
						zap.Error(err),
					)
					continue
				}
				tzTaskers[tz] = tasker.New(tasker.Option{Tz: tz, Verbose: false})
			}
			t := tzTaskers[tz]

			t.Task(capturedSchedule.Cron, func(ctx context.Context) (int, error) {
				if err := m.executeSchedule(
					ctx,
					capturedAppName,
					capturedScheduleName,
					capturedApp,
					capturedRecipe,
					capturedRecipePath,
					capturedSchedule,
				); err != nil {
					zap.L().Error("scheduler: schedule execution failed",
						zap.String("app", capturedAppName),
						zap.String("schedule", capturedScheduleName),
						zap.Error(err),
					)
					return 1, err
				}
				return 0, nil
			})

			m.count++
			m.entries = append(m.entries, ScheduleEntry{
				AppName:      capturedAppName,
				ScheduleName: capturedScheduleName,
				RecipePath:   capturedRecipePath,
				Schedule:     capturedSchedule,
				App:          capturedApp,
			})
			zap.L().Info("scheduler: registered cron task",
				zap.String("app", capturedAppName),
				zap.String("schedule", capturedScheduleName),
				zap.String("cron", capturedSchedule.Cron),
				zap.String("tz", tz),
			)
		}
	}

	for _, t := range tzTaskers {
		m.taskers = append(m.taskers, t)
	}
}

// executeSchedule runs the recipe for a single cron trigger, mirroring the webhook
// handler pattern: search hosts, build run params, submit to queue async.
func (m *Manager) executeSchedule(
	ctx context.Context,
	appName, scheduleName string,
	app apps.AppConfig,
	recipe cuetry.Recipe,
	recipePath string,
	sched cuetry.RecipeSchedule,
) error {
	pluginMgr, err := plugins.Open(ctx, m.opts.Config)
	if err != nil {
		return fmt.Errorf("open plugins: %w", err)
	}

	// Resolve target hosts.
	searchIn := &hostapi.SearchHostsInput{
		ConfigPath: m.opts.ConfigPath,
		Config:     m.opts.Config,
		Name:       app.Target,
		Providers:  app.Provider,
		Backends:   app.Backend,
	}
	if app.Target == "" && app.TargetRegex != "" {
		searchIn.NameRegex = app.TargetRegex
	}

	searchOut, err := hostapi.SearchHosts(ctx, searchIn, m.opts.ExecRegistry, m.opts.SearchRegistry)
	if err != nil {
		_ = pluginMgr.Close()
		return fmt.Errorf("search hosts: %w", err)
	}

	if len(searchOut.Records) == 0 {
		_ = pluginMgr.Close()
		return fmt.Errorf("no target hosts found for app %q", appName)
	}

	// Merge schedule-level env on top of any global defaults.
	cliEnv := make(map[string]string, len(sched.Env))
	maps.Copy(cliEnv, sched.Env)
	cliEnv, err = cuetry.ValidateAndApplyPromptDefaults(recipe.PromptDefs(), cliEnv)
	if err != nil {
		return fmt.Errorf("prompt validation: %w", err)
	}

	secRes, _ := cuetry.NewSecretResolverWithPlugins(
		cuetry.SecretResolverOptionsFromHoney(m.opts.Config), pluginMgr,
	)
	aiPrompt := ui.LoadAISystemPromptFromConfigPath(m.opts.ConfigPath)

	sshUser := ""
	if m.opts.Config != nil && m.opts.Config.Defaults.SSHUser != "" {
		sshUser = m.opts.Config.Defaults.SSHUser
	}

	runParams := engine.CueRecipeRunParams{
		Recipe:         recipe,
		RecipeDir:      filepath.Dir(recipePath),
		Records:        searchOut.Records,
		SSHUser:        sshUser,
		CLIEnv:         cliEnv,
		ConfigPath:     m.opts.ConfigPath,
		AISystemPrompt: aiPrompt,
		SecretResolver: secRes,
		PluginMgr:      pluginMgr,
		Execute:        true,
		Obs:            m.opts.Metrics,
		Reg:            m.opts.ExecRegistry,
		Pools:          m.opts.Pools,
		Cache:          m.opts.Cache,
	}

	// Build an optional session recorder.
	var rec *engine.SessionRecorder
	if strings.TrimSpace(m.opts.RecordDir) != "" {
		trigger := fmt.Sprintf("cron-%s-%s", appName, scheduleName)
		rec, err = engine.NewBatchSessionRecorder(m.opts.RecordDir, trigger, sshUser, len(searchOut.Records))
		if err != nil {
			zap.L().Warn("scheduler: could not create session recorder",
				zap.String("app", appName),
				zap.String("schedule", scheduleName),
				zap.Error(err),
			)
			rec = nil
		} else {
			hash, _ := recipe.HashJSON()
			rec.RecordRecipeMeta(engine.RecipeMeta{
				RecipePath:        recipePath,
				HostCount:         len(searchOut.Records),
				RecipeContentHash: hash,
				StartedAt:         time.Now().UTC(),
				Hosts:             engine.HostsForRecipeMeta(searchOut.Records, 100),
			})
		}
	}

	// Detach from the per-tick gronx context so the queue goroutine is not
	// cancelled when the task function returns (gronx cancels tick contexts).
	runCtx := context.WithoutCancel(ctx)

	// Always submit async; if queue is nil, run inline (dev / no-queue mode).
	run := func() {
		defer func() {
			_ = pluginMgr.Close()
			if rec != nil {
				_ = rec.Close()
			}
		}()

		ch := make(chan engine.HostExecResult, 64)
		go func() {
			defer close(ch)
			_ = engine.StreamCueRecipeSteps(runCtx, runParams, ch)
		}()

		for res := range ch {
			if rec != nil {
				rec.RecordHostExecResult(res)
			}
		}
	}

	if m.opts.Queue != nil {
		if qErr := m.opts.Queue.Submit(run); qErr != nil {
			if errors.Is(qErr, queue.ErrQueueFull) {
				zap.L().Warn("scheduler: queue full, skipping tick",
					zap.String("app", appName),
					zap.String("schedule", scheduleName),
				)
				_ = pluginMgr.Close()
				if rec != nil {
					_ = rec.Close()
				}
				return nil
			}
			_ = pluginMgr.Close()
			if rec != nil {
				_ = rec.Close()
			}
			return fmt.Errorf("submit to queue: %w", qErr)
		}
		return nil
	}

	// Inline fallback (blocking).
	run()
	return nil
}

// LoadScheduleEntries reads all AppTypeRecipe apps from cfg, parses their CUE recipes,
// and returns one ScheduleEntry per declared schedule. Errors on individual apps are
// logged and skipped; only a nil cfg returns nil,nil.
func LoadScheduleEntries(cfg *config.File, configPath string) ([]ScheduleEntry, error) {
	if cfg == nil {
		return nil, nil
	}
	// Reuse register() logic via a temporary manager.
	// No taskers are needed — we only want the parsed entries.
	m := &Manager{opts: Options{Config: cfg, ConfigPath: configPath}}
	m.register()
	return m.entries, nil
}
