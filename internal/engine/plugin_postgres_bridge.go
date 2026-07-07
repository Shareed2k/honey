package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/plugins"
	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
	"github.com/shareed2k/honey/internal/postgres"
	"github.com/shareed2k/honey/internal/safepath"
	"github.com/shareed2k/honey/internal/stepkv"
	"go.uber.org/zap"
)

type pluginPostgresBridge struct {
	h     *plugins.HostRunContext
	pools *postgres.PoolManager
}

// NewPostgresBridge returns a PostgresBridge for one plugin host invocation.
// NewPostgresBridge ...
func NewPostgresBridge(h *plugins.HostRunContext, pools *postgres.PoolManager) plugins.PostgresBridge {
	return &pluginPostgresBridge{h: h, pools: pools}
}

func (b *pluginPostgresBridge) Query(ctx context.Context, in apiv1.PostgresSQLInput) apiv1.PostgresOutput {
	return b.runSQL(ctx, in, true)
}

func (b *pluginPostgresBridge) Exec(ctx context.Context, in apiv1.PostgresSQLInput) apiv1.PostgresOutput {
	return b.runSQL(ctx, in, false)
}

func (b *pluginPostgresBridge) runSQL(ctx context.Context, in apiv1.PostgresSQLInput, queryPath bool) apiv1.PostgresOutput {
	dsn, err := plugins.ResolvePostgresDSN(ctx, b.h, in.DSNSecret)
	if err != nil {
		return apiv1.PostgresOutput{Failed: true, Error: err.Error()}
	}
	dsn, err = b.rewritePostgresDSN(dsn, in)
	if err != nil {
		return apiv1.PostgresOutput{Failed: true, Error: err.Error()}
	}
	zap.L().Debug(
		"plugin postgres query",
		zap.String("plugin_id", b.h.PluginID),
		zap.String("host_name", b.h.Record.Name),
		zap.String("tunnel_step", in.TunnelStep),
		zap.Int("timeout_ms", in.TimeoutMS),
	)
	args, err := postgres.ParseParams(in.Params)
	if err != nil {
		return apiv1.PostgresOutput{Failed: true, Error: err.Error()}
	}
	timeout := clampPostgresTimeout(in.TimeoutMS, b.h.MaxPostgresTimeoutMS)
	readonly := postgresReadonlyDefault(in.Readonly)
	pools := b.pools
	pluginID := b.h.PluginID
	hostName := b.h.Record.Name

	if queryPath {
		res, err := postgres.Query(ctx, pools, dsn, in.SQL, args, postgres.QueryOpts{
			Timeout: timeout, PluginID: pluginID, HostName: hostName,
		})
		if err != nil {
			return apiv1.PostgresOutput{Failed: true, Error: err.Error()}
		}
		out := rowsOutput(res.Rows)
		return b.storeKVResults(ctx, in, out)
	}

	res, err := postgres.Exec(ctx, pools, dsn, in.SQL, args, postgres.ExecOpts{
		Timeout: timeout, Readonly: readonly, PluginID: pluginID, HostName: hostName,
	})
	if err != nil {
		return apiv1.PostgresOutput{Failed: true, Error: err.Error()}
	}
	out := apiv1.PostgresOutput{Changed: true, RowsAffected: res.RowsAffected}
	if res.RowsAffected > 0 {
		b, _ := json.Marshal(map[string]int64{"rows_affected": res.RowsAffected})
		out.Stdout = string(b)
	}
	return b.storeKVResults(ctx, in, out)
}

func (b *pluginPostgresBridge) Migrate(ctx context.Context, in apiv1.PostgresMigrateInput) apiv1.PostgresOutput {
	dsn, err := plugins.ResolvePostgresDSN(ctx, b.h, in.DSNSecret)
	if err != nil {
		return apiv1.PostgresOutput{Failed: true, Error: err.Error()}
	}
	// migrate does not use tunnel_step rewrite
	dir, err := resolveMigrationsDir(b.h.RecipeDir, in.MigrationsDir)
	if err != nil {
		return apiv1.PostgresOutput{Failed: true, Error: err.Error()}
	}
	files, err := resolveMigrationFiles(b.h.RecipeDir, in.Files)
	if err != nil {
		return apiv1.PostgresOutput{Failed: true, Error: err.Error()}
	}
	timeout := clampPostgresTimeout(in.TimeoutMS, b.h.MaxPostgresTimeoutMS)
	res, err := postgres.Migrate(ctx, b.pools, dsn, dir, files, postgres.MigrateOpts{
		Timeout: timeout, PluginID: b.h.PluginID, HostName: b.h.Record.Name,
	})
	if err != nil {
		return apiv1.PostgresOutput{Failed: true, Error: err.Error()}
	}
	out := apiv1.PostgresOutput{Changed: true, Stdout: res.Stdout}
	if strings.TrimSpace(in.KVKey) != "" {
		sqlIn := apiv1.PostgresSQLInput{
			KVKey: in.KVKey, KVKeyPerHost: in.KVKeyPerHost, Extract: in.Extract,
		}
		return b.storeKVResults(ctx, sqlIn, out)
	}
	return out
}

func (b *pluginPostgresBridge) storeKVResults(ctx context.Context, in apiv1.PostgresSQLInput, out apiv1.PostgresOutput) apiv1.PostgresOutput {
	if out.Failed || out.Error != "" {
		return out
	}
	baseKey, err := cuetry.ResolvePostgresKVBaseKey(in.KVKey, in.KVKeyPerHost, b.h.Record.Name)
	if err != nil {
		return apiv1.PostgresOutput{Failed: true, Error: err.Error()}
	}
	if baseKey == "" && len(in.Extract) == 0 {
		return out
	}
	sess, ok := plugins.KVSessionFromContext(ctx)
	if !ok || sess == nil {
		return apiv1.PostgresOutput{Failed: true, Error: "postgres kv: stepkv session not available"}
	}
	stdout := strings.TrimSpace(out.Stdout)
	if baseKey != "" && stdout != "" {
		if err := putPostgresKV(sess, baseKey, stdout); err != nil {
			return apiv1.PostgresOutput{Failed: true, Error: err.Error()}
		}
		postgres.LogAudit(postgres.AuditEvent{
			PluginID: b.h.PluginID, HostName: b.h.Record.Name, Action: "kv_put", SQL: baseKey,
		})
	}
	if len(in.Extract) == 0 {
		return out
	}
	if stdout == "" {
		return apiv1.PostgresOutput{Failed: true, Error: "postgres extract: query stdout is empty"}
	}
	if baseKey == "" {
		return apiv1.PostgresOutput{Failed: true, Error: "postgres extract requires kv_key"}
	}
	for name, query := range in.Extract {
		val, err := cuetry.EvalJQ(stdout, query)
		if err != nil {
			return apiv1.PostgresOutput{Failed: true, Error: fmt.Sprintf("postgres extract %q: %v", name, err)}
		}
		key, err := cuetry.PostgresExtractKVKey(baseKey, name)
		if err != nil {
			return apiv1.PostgresOutput{Failed: true, Error: err.Error()}
		}
		if err := putPostgresKV(sess, key, val); err != nil {
			return apiv1.PostgresOutput{Failed: true, Error: err.Error()}
		}
	}
	return out
}

func (b *pluginPostgresBridge) rewritePostgresDSN(dsn string, in apiv1.PostgresSQLInput) (string, error) {
	hostOverride := strings.TrimSpace(in.Host)
	portOverride := strings.TrimSpace(in.Port)
	if ts := strings.TrimSpace(in.TunnelStep); ts != "" && b.h.TunnelCoord != nil {
		th, tp, ok := b.h.TunnelCoord.LookupEndpoint(ts, b.h.SSHUser, b.h.Record)
		if !ok {
			zap.L().Debug(
				"plugin postgres dsn rewrite tunnel miss",
				zap.String("tunnel_step", ts),
				zap.String("host_name", b.h.Record.Name),
			)
			return "", fmt.Errorf("postgres tunnel_step %q not found for host %q", ts, b.h.Record.Name)
		}
		hostOverride = th
		if portOverride == "" {
			portOverride = strconv.Itoa(tp)
		}
		zap.L().Debug(
			"plugin postgres dsn rewrite",
			zap.String("tunnel_step", ts),
			zap.String("host_name", b.h.Record.Name),
			zap.Bool("found", true),
			zap.String("rewritten_host", hostOverride),
			zap.String("rewritten_port", portOverride),
		)
	}
	return postgres.RewriteDSNHostPort(dsn, hostOverride, portOverride)
}

func putPostgresKV(sess *stepkv.Session, key, value string) error {
	if err := sess.Put(key, value); err != nil {
		if err == stepkv.ErrValueTooLong {
			return fmt.Errorf("postgres kv: value for key %q exceeds max size", key)
		}
		return fmt.Errorf("postgres kv put %q: %w", key, err)
	}
	return nil
}

func rowsOutput(rows []map[string]any) apiv1.PostgresOutput {
	if len(rows) == 0 {
		return apiv1.PostgresOutput{Changed: false, Rows: []map[string]any{}, Stdout: "[]"}
	}
	b, err := json.Marshal(rows)
	if err != nil {
		return apiv1.PostgresOutput{Failed: true, Error: err.Error()}
	}
	return apiv1.PostgresOutput{Changed: true, Rows: rows, Stdout: string(b)}
}

func clampPostgresTimeout(ms, maxMS int) time.Duration {
	if ms <= 0 {
		return 0
	}
	if maxMS > 0 && ms > maxMS {
		ms = maxMS
	}
	return time.Duration(ms) * time.Millisecond
}

func postgresReadonlyDefault(p *bool) bool {
	if p == nil {
		return true
	}
	return *p
}

// MergeRecipeSecretRefs ...
func MergeRecipeSecretRefs(defaults *cuetry.RecipeDefaults, step cuetry.Step) map[string]string {
	out := make(map[string]string)
	if defaults != nil {
		for k, v := range defaults.Secrets {
			out[k] = v.StringRef()
		}
	}
	for k, v := range step.Base().Secrets {
		out[k] = v.StringRef()
	}
	return out
}

func resolveMigrationsDir(recipeDir, rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", nil
	}
	if filepath.IsAbs(rel) {
		return filepath.Clean(rel), nil
	}
	if strings.TrimSpace(recipeDir) == "" {
		return "", fmt.Errorf("empty recipe directory")
	}
	abs := filepath.Clean(filepath.Join(recipeDir, rel))
	if err := safepath.Under(recipeDir, abs); err != nil {
		return "", err
	}
	return abs, nil
}

func resolveMigrationFiles(recipeDir string, files []string) ([]string, error) {
	if len(files) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if !filepath.IsAbs(f) {
			if strings.TrimSpace(recipeDir) == "" {
				return nil, fmt.Errorf("empty recipe directory")
			}
			f = filepath.Join(recipeDir, f)
		}
		f = filepath.Clean(f)
		if err := safepath.Under(recipeDir, f); err != nil {
			return nil, fmt.Errorf("migration file outside recipe dir: %w", err)
		}
		out = append(out, f)
	}
	return out, nil
}
