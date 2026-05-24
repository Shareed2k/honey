package ui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/plugins"
	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
	"github.com/shareed2k/honey/internal/stepkv"
)

func TestResolvePostgresKVBaseKey(t *testing.T) {
	t.Parallel()
	key, err := cuetry.ResolvePostgresKVBaseKey("pg_activity", true, "db/primary:1")
	if err != nil {
		t.Fatal(err)
	}
	if key != "pg_activity_db_primary_1" {
		t.Fatalf("got %q", key)
	}
}

func TestPluginPostgresBridge_rewriteDSN_tunnelStep(t *testing.T) {
	t.Parallel()
	pool := NewGlobalTunnelPool(0)
	defer pool.Close()
	coord := NewRecipeTunnelCoordinator(pool)
	defer coord.Close()

	rec := hosts.Record{Name: "db1", PrimaryIP: "10.0.0.1", Provider: "gcp"}
	coord.Register("pg_tunnel", "ubuntu", rec, TunnelEndpoint{
		Host: "127.0.0.1", Port: 15432, Mode: "local",
	}, nil)

	b := &pluginPostgresBridge{h: &plugins.HostRunContext{
		SSHUser:     "ubuntu",
		Record:      rec,
		TunnelCoord: coord,
	}}
	out, err := b.rewritePostgresDSN("postgres://u:p@remote.db:5432/app?sslmode=require", apiv1.PostgresSQLInput{
		TunnelStep: "pg_tunnel",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "127.0.0.1") || !strings.Contains(out, "15432") {
		t.Fatalf("got %q", out)
	}
}

func TestPluginPostgresBridge_rewriteDSN_hostPortOverride(t *testing.T) {
	t.Parallel()
	b := &pluginPostgresBridge{h: &plugins.HostRunContext{}}
	out, err := b.rewritePostgresDSN("postgres://u:p@remote.db:5432/app", apiv1.PostgresSQLInput{
		Host: "127.0.0.1",
		Port: "5433",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "127.0.0.1") || !strings.Contains(out, "5433") {
		t.Fatalf("got %q", out)
	}
}

func TestPluginPostgresBridge_storeKV(t *testing.T) {
	t.Parallel()
	sess, err := stepkv.Start(0)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	ctx := plugins.WithKVSession(context.Background(), sess)
	h := &plugins.HostRunContext{
		PluginID: "postgres",
		Record:   hosts.Record{Name: "db1"},
	}
	b := NewPluginPostgresBridge(h)
	in := apiv1.PostgresSQLInput{
		KVKey:        "pg_activity",
		KVKeyPerHost: true,
		Extract:      map[string]string{"count": ".[0].n"},
	}
	out := apiv1.PostgresOutput{Changed: true, Stdout: `[{"n":4}]`}
	got := b.(*pluginPostgresBridge).storeKVResults(ctx, in, out)
	if got.Failed || got.Error != "" {
		t.Fatalf("store failed: %+v", got)
	}
	v, ok, err := sess.Get("pg_activity_db1")
	if err != nil || !ok {
		t.Fatalf("full json missing: ok=%v err=%v", ok, err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(v), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("json %q", v)
	}
	v2, ok, err := sess.Get("pg_activity_db1_count")
	if err != nil || !ok || v2 != "4" {
		t.Fatalf("extract got %q ok=%v err=%v", v2, ok, err)
	}
}
