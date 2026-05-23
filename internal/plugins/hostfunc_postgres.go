package plugins

import (
	"context"
	"fmt"
	"strings"

	extism "github.com/extism/go-sdk"

	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
)

func postgresQueryCallback(_ string) extism.HostFunctionStackCallback {
	return func(ctx context.Context, p *extism.CurrentPlugin, stack []uint64) {
		stack[0] = writeRemoteJSON(p, runPostgresQueryFromHost(ctx, readHostInput(p, stack)))
	}
}

func postgresExecCallback(_ string) extism.HostFunctionStackCallback {
	return func(ctx context.Context, p *extism.CurrentPlugin, stack []uint64) {
		stack[0] = writeRemoteJSON(p, runPostgresExecFromHost(ctx, readHostInput(p, stack)))
	}
}

func postgresMigrateCallback(_ string) extism.HostFunctionStackCallback {
	return func(ctx context.Context, p *extism.CurrentPlugin, stack []uint64) {
		stack[0] = writeRemoteJSON(p, runPostgresMigrateFromHost(ctx, readHostInput(p, stack)))
	}
}

func runPostgresQueryFromHost(ctx context.Context, raw string) any {
	var in apiv1.PostgresSQLInput
	if err := parseRemoteInput(raw, &in); err != nil {
		return apiv1.PostgresOutput{Failed: true, Error: err.Error()}
	}
	return runPostgresQuery(ctx, in)
}

func runPostgresExecFromHost(ctx context.Context, raw string) any {
	var in apiv1.PostgresSQLInput
	if err := parseRemoteInput(raw, &in); err != nil {
		return apiv1.PostgresOutput{Failed: true, Error: err.Error()}
	}
	return runPostgresExec(ctx, in)
}

func runPostgresMigrateFromHost(ctx context.Context, raw string) any {
	var in apiv1.PostgresMigrateInput
	if err := parseRemoteInput(raw, &in); err != nil {
		return apiv1.PostgresOutput{Failed: true, Error: err.Error()}
	}
	return runPostgresMigrate(ctx, in)
}

func runPostgresQuery(ctx context.Context, in apiv1.PostgresSQLInput) apiv1.PostgresOutput {
	h, err := remoteHostCtx(ctx)
	if err != nil {
		return apiv1.PostgresOutput{Failed: true, Error: err.Error()}
	}
	if err := validatePostgresSQLInput(in); err != nil {
		return apiv1.PostgresOutput{Failed: true, Error: err.Error()}
	}
	if !h.Execute {
		return apiv1.PostgresOutput{
			Changed: true,
			Stdout:  postgresDryRunPlan("would postgres query", h, in.SQL, in),
		}
	}
	if h.Postgres == nil {
		return apiv1.PostgresOutput{Failed: true, Error: "postgres bridge not configured"}
	}
	return h.Postgres.Query(ctx, in)
}

func runPostgresExec(ctx context.Context, in apiv1.PostgresSQLInput) apiv1.PostgresOutput {
	h, err := remoteHostCtx(ctx)
	if err != nil {
		return apiv1.PostgresOutput{Failed: true, Error: err.Error()}
	}
	if err := validatePostgresSQLInput(in); err != nil {
		return apiv1.PostgresOutput{Failed: true, Error: err.Error()}
	}
	if !h.Execute {
		return apiv1.PostgresOutput{
			Changed: true,
			Stdout:  postgresDryRunPlan("would postgres exec", h, in.SQL, in),
		}
	}
	if h.Postgres == nil {
		return apiv1.PostgresOutput{Failed: true, Error: "postgres bridge not configured"}
	}
	return h.Postgres.Exec(ctx, in)
}

func runPostgresMigrate(ctx context.Context, in apiv1.PostgresMigrateInput) apiv1.PostgresOutput {
	h, err := remoteHostCtx(ctx)
	if err != nil {
		return apiv1.PostgresOutput{Failed: true, Error: err.Error()}
	}
	if err := validatePostgresMigrateInput(in, h); err != nil {
		return apiv1.PostgresOutput{Failed: true, Error: err.Error()}
	}
	if !h.Execute {
		dir := strings.TrimSpace(in.MigrationsDir)
		if dir == "" && len(in.Files) > 0 {
			dir = strings.Join(in.Files, ", ")
		}
		return apiv1.PostgresOutput{
			Changed: true,
			Stdout: postgresDryRunPlan("would postgres migrate "+dir, h, "", apiv1.PostgresSQLInput{
				KVKey: in.KVKey, KVKeyPerHost: in.KVKeyPerHost, Extract: in.Extract,
			}),
		}
	}
	if h.Postgres == nil {
		return apiv1.PostgresOutput{Failed: true, Error: "postgres bridge not configured"}
	}
	return h.Postgres.Migrate(ctx, in)
}

func validatePostgresSQLInput(in apiv1.PostgresSQLInput) error {
	if strings.TrimSpace(in.DSNSecret) == "" {
		return fmt.Errorf("dsn_secret is required")
	}
	if strings.TrimSpace(in.SQL) == "" {
		return fmt.Errorf("sql is required")
	}
	if in.TimeoutMS <= 0 {
		return fmt.Errorf("timeout_ms is required")
	}
	if err := validatePostgresKVConfig(in.KVKey, in.KVKeyPerHost, in.Extract); err != nil {
		return err
	}
	return nil
}

func validatePostgresMigrateInput(in apiv1.PostgresMigrateInput, h *HostRunContext) error {
	if strings.TrimSpace(in.DSNSecret) == "" {
		return fmt.Errorf("dsn_secret is required")
	}
	if in.TimeoutMS <= 0 {
		return fmt.Errorf("timeout_ms is required")
	}
	if strings.TrimSpace(in.MigrationsDir) == "" && len(in.Files) == 0 {
		return fmt.Errorf("migrations_dir or files is required")
	}
	if postgresReadonly(in.Readonly) {
		return fmt.Errorf("readonly mode rejects migrate")
	}
	if err := validatePostgresKVConfig(in.KVKey, in.KVKeyPerHost, in.Extract); err != nil {
		return err
	}
	if !h.Execute {
		return nil
	}
	return nil
}

func postgresDryRunPlan(prefix string, h *HostRunContext, sql string, in apiv1.PostgresSQLInput) string {
	msg := dryRunPlan(prefix, h)
	if s := strings.TrimSpace(sql); s != "" {
		if len(s) > 80 {
			s = s[:80] + "…"
		}
		msg += ": " + s
	}
	if key := strings.TrimSpace(in.KVKey); key != "" {
		msg += fmt.Sprintf("; would kv put %q", key)
		if in.KVKeyPerHost {
			msg += " (per-host)"
		}
	}
	for name := range in.Extract {
		msg += fmt.Sprintf("; would kv extract %q", name)
	}
	return msg
}

func postgresReadonly(p *bool) bool {
	if p == nil {
		return true
	}
	return *p
}

// RunPostgresQueryForTest exposes postgres_query for unit tests.
func RunPostgresQueryForTest(ctx context.Context, in apiv1.PostgresSQLInput) apiv1.PostgresOutput {
	return runPostgresQuery(ctx, in)
}

// RunPostgresExecForTest exposes postgres_exec for unit tests.
func RunPostgresExecForTest(ctx context.Context, in apiv1.PostgresSQLInput) apiv1.PostgresOutput {
	return runPostgresExec(ctx, in)
}
