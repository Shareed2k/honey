package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/postgres"
)

// StreamCueStepPostgres ...
func init() {
	RegisterStepExecutor(cuetry.KindPostgres, &PostgresExecutor{})
}

// PostgresExecutor executes the corresponding recipe step.
type PostgresExecutor struct{}

// ExecuteDryRun executes a dry run of the step.
func (e *PostgresExecutor) ExecuteDryRun(_ context.Context, _ ExecutionRequest, _ ExecutionOptions, _ io.Writer) error {
	return nil
}

// ExecuteStream streams the step execution.
func (e *PostgresExecutor) ExecuteStream(ctx context.Context, req ExecutionRequest, opts ExecutionOptions, resCh chan<- HostExecResult) error {
	step, targets, ch, retryCfg, attemptMax := req.Step, req.Targets, resCh, req.RetryCfg, req.AttemptMax
	pgs, _ := step.(*cuetry.PostgresStep)
	if pgs == nil || pgs.Postgres == nil {
		return fmt.Errorf("internal: postgres step missing postgres field")
	}
	p := pgs.Postgres
	if p == nil {
		return fmt.Errorf("internal: postgres step missing config")
	}

	execOne := func(tc TargetContext) HostExecResult {
		r := tc.Record
		outcome := RunHostExecWithRetry(ctx, retryCfg, func() HostExecResult {
			res := HostExecResult{
				Name:     r.Name,
				IP:       r.PrimaryIP,
				Provider: r.Provider,
			}

			// 1. Resolve DSN Secret
			dsn, err := resolvePostgresDSN(ctx, opts, step, p.DSNSecret)
			if err != nil {
				res.Success = false
				res.ErrMsg = fmt.Sprintf("dsn resolve error: %s", err.Error())
				return res
			}

			// 2. Rewrite DSN if TunnelStep is set
			tunnelStep := strings.TrimSpace(p.TunnelStep)
			if tunnelStep != "" {
				ep, ok := opts.TunnelCoord.Lookup(tunnelStep, opts.SSHUser, r)
				if !ok {
					res.Success = false
					res.ErrMsg = fmt.Sprintf("active tunnel %q not found", tunnelStep)
					return res
				}
				dsn, err = postgres.RewriteDSNHostPort(dsn, ep.Host, strconv.Itoa(ep.Port))
				if err != nil {
					res.Success = false
					res.ErrMsg = fmt.Sprintf("dsn rewrite error: %s", err.Error())
					return res
				}
			}

			// 3. Parse Parameters
			args, err := postgres.ParseParams(p.Params)
			if err != nil {
				res.Success = false
				res.ErrMsg = fmt.Sprintf("params parse error: %s", err.Error())
				return res
			}

			// 4. Perform Postgres Operation
			action := strings.ToLower(strings.TrimSpace(p.Action))
			readonly := p.Readonly != nil && *p.Readonly
			timeout := 30 * time.Second
			if p.TimeoutMS > 0 {
				timeout = time.Duration(p.TimeoutMS) * time.Millisecond
			}

			switch action {
			case "query":
				dbRes, err := postgres.Query(ctx, opts.Pools, dsn, p.SQL, args, postgres.QueryOpts{
					Timeout:  timeout,
					HostName: r.Name,
					DryRun:   !opts.Execute,
				})
				if err != nil {
					res.Success = false
					res.ErrMsg = err.Error()
					return res
				}
				res.Success = true
				if len(dbRes.Rows) > 0 {
					b, _ := json.Marshal(dbRes.Rows)
					res.Output = string(b)
				}

			case "exec":
				dbRes, err := postgres.Exec(ctx, opts.Pools, dsn, p.SQL, args, postgres.ExecOpts{
					Timeout:  timeout,
					Readonly: readonly,
					HostName: r.Name,
					DryRun:   !opts.Execute,
				})
				if err != nil {
					res.Success = false
					res.ErrMsg = err.Error()
					return res
				}
				res.Success = true
				res.Changed = dbRes.RowsAffected > 0
				b, _ := json.Marshal(map[string]int64{"rows_affected": dbRes.RowsAffected})
				res.Output = string(b)

			case "migrate":
				var files []string
				if len(p.Files) > 0 {
					for _, f := range p.Files {
						files = append(files, filepath.Join(opts.RecipeDir, f))
					}
				}
				migrationsDir := ""
				if p.MigrationsDir != "" {
					migrationsDir = filepath.Join(opts.RecipeDir, p.MigrationsDir)
				}

				dbRes, err := postgres.Migrate(ctx, opts.Pools, dsn, migrationsDir, files, postgres.MigrateOpts{
					Timeout:  timeout,
					Readonly: readonly,
					HostName: r.Name,
					DryRun:   !opts.Execute,
				})
				if err != nil {
					res.Success = false
					res.ErrMsg = err.Error()
					return res
				}
				res.Success = true
				res.Changed = len(dbRes.Applied) > 0
				res.Output = dbRes.Stdout
			}

			return res
		})
		RecordMaxAttempts(attemptMax, outcome.Attempts)
		return outcome.Result
	}

	for _, target := range targets {
		res := execOne(target)
		if res.Success && p.Output != "" && opts.OutputCapture != nil {
			opts.OutputCapture.Set(p.Output, res.Output)
		}
		ch <- res
	}
	return nil
}

func resolvePostgresDSN(ctx context.Context, opts ExecutionOptions, step cuetry.Step, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("dsn_secret is required")
	}
	secureRef := ref
	if !strings.HasPrefix(ref, "secure:v1:") {
		var v string
		var ok bool
		if step.Base().Secrets != nil {
			v, ok = step.Base().Secrets[ref]
		}
		if !ok && opts.Recipe.Defaults != nil && opts.Recipe.Defaults.Secrets != nil {
			v, ok = opts.Recipe.Defaults.Secrets[ref]
		}
		if !ok {
			return "", fmt.Errorf("unknown secrets key %q", ref)
		}
		secureRef = strings.TrimSpace(v)
	}
	if !strings.HasPrefix(secureRef, "secure:v1:") {
		return "", fmt.Errorf("secrets key %q must reference secure:v1", ref)
	}
	if opts.SecretResolver == nil {
		return "", fmt.Errorf("secret resolver not configured")
	}
	return opts.SecretResolver.Resolve(ctx, secureRef)
}
