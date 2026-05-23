package plugins

import (
	"context"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
)

type fakePostgresBridge struct {
	lastQuery apiv1.PostgresSQLInput
	called    bool
}

func (f *fakePostgresBridge) Query(_ context.Context, in apiv1.PostgresSQLInput) apiv1.PostgresOutput {
	f.called = true
	f.lastQuery = in
	return apiv1.PostgresOutput{Changed: true, Rows: []map[string]any{{"n": 1}}, Stdout: `[{"n":1}]`}
}

func (f *fakePostgresBridge) Exec(context.Context, apiv1.PostgresSQLInput) apiv1.PostgresOutput {
	return apiv1.PostgresOutput{Changed: true}
}

func (f *fakePostgresBridge) Migrate(context.Context, apiv1.PostgresMigrateInput) apiv1.PostgresOutput {
	return apiv1.PostgresOutput{Changed: true, Stdout: "applied"}
}

func TestHostFunctions_postgresGated(t *testing.T) {
	t.Parallel()
	none := hostFunctionNames(Manifest{ID: "x"})
	for _, name := range []string{"postgres_query", "postgres_exec", "postgres_migrate"} {
		if slicesContains(none, name) {
			t.Fatalf("%s present without allow_postgres", name)
		}
	}
	with := hostFunctionNames(Manifest{ID: "x", AllowPostgres: true})
	for _, name := range []string{"postgres_query", "postgres_exec", "postgres_migrate"} {
		if !slicesContains(with, name) {
			t.Fatalf("expected %s when allow_postgres is true", name)
		}
	}
}

func TestRunPostgresQuery_dryRunKV(t *testing.T) {
	t.Parallel()
	ctx := WithHostRunContext(t.Context(), &HostRunContext{
		Execute: false,
		Record:  hosts.Record{Name: "db1"},
	})
	out := RunPostgresQueryForTest(ctx, apiv1.PostgresSQLInput{
		DSNSecret: "PG_DSN",
		SQL:       "SELECT 1",
		TimeoutMS: 5000,
		KVKey:     "pg_activity",
		Extract:   map[string]string{"count": ".[0].n"},
	})
	if !strings.Contains(out.Stdout, "would kv put") || !strings.Contains(out.Stdout, "count") {
		t.Fatalf("got %+v", out)
	}
}

func TestRunPostgresQuery_dryRun(t *testing.T) {
	t.Parallel()
	ctx := WithHostRunContext(t.Context(), &HostRunContext{
		Execute: false,
		Record:  hosts.Record{Name: "db1"},
	})
	out := RunPostgresQueryForTest(ctx, apiv1.PostgresSQLInput{
		DSNSecret: "PG_DSN",
		SQL:       "SELECT 1",
		TimeoutMS: 5000,
	})
	if !out.Changed || out.Failed || !strings.Contains(out.Stdout, "db1") {
		t.Fatalf("got %+v", out)
	}
}

func TestRunPostgresQuery_execute(t *testing.T) {
	t.Parallel()
	bridge := &fakePostgresBridge{}
	ctx := WithHostRunContext(t.Context(), &HostRunContext{
		Execute:  true,
		Record:   hosts.Record{Name: "db1"},
		Postgres: bridge,
	})
	out := RunPostgresQueryForTest(ctx, apiv1.PostgresSQLInput{
		DSNSecret: "PG_DSN",
		SQL:       "SELECT 1",
		TimeoutMS: 5000,
	})
	if !bridge.called || !out.Changed {
		t.Fatalf("bridge=%v out=%+v", bridge, out)
	}
}

func TestResolvePostgresDSN_secretsKey(t *testing.T) {
	t.Parallel()
	var resolved string
	h := &HostRunContext{
		Execute: true,
		RecipeSecrets: map[string]string{
			"PG_DSN": "secure:v1:abc:def",
		},
		ResolveSecret: func(_ context.Context, ref string) (string, error) {
			resolved = ref
			return "postgres://local/test", nil
		},
	}
	got, err := ResolvePostgresDSN(t.Context(), h, "PG_DSN")
	if err != nil || got != "postgres://local/test" || resolved != "secure:v1:abc:def" {
		t.Fatalf("got=%q err=%v resolved=%q", got, err, resolved)
	}
}
