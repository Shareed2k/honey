package engine

import (
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/stepkv"
)

// TestWritePluginKVResult_StoresStdout mirrors
// TestPluginPostgresBridge_storeKV's style: a real (in-process) stepkv
// session, direct call to the function under test, verified via sess.Get.
func TestWritePluginKVResult_StoresStdout(t *testing.T) {
	t.Parallel()
	sess, err := stepkv.Start(0)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	pl := &cuetry.RecipeStepPlugin{ID: "duckdb", Action: "query", KVKey: "result", KVKeyPerHost: true}
	if err := writePluginKVResult(sess, pl, "db1", `{"rows":[1,2,3]}`); err != nil {
		t.Fatalf("writePluginKVResult: %v", err)
	}
	v, ok, err := sess.Get("result_db1")
	if err != nil || !ok {
		t.Fatalf("kv missing: ok=%v err=%v", ok, err)
	}
	if v != `{"rows":[1,2,3]}` {
		t.Fatalf("got %q", v)
	}
}

// TestWritePluginKVResult_NoopWithoutKey confirms plugins without kv_key set
// (the common case) never touch the KV store at all.
func TestWritePluginKVResult_NoopWithoutKey(t *testing.T) {
	t.Parallel()
	sess, err := stepkv.Start(0)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	pl := &cuetry.RecipeStepPlugin{ID: "duckdb", Action: "query"}
	if err := writePluginKVResult(sess, pl, "db1", "anything"); err != nil {
		t.Fatalf("expected no-op, got err: %v", err)
	}
	if _, ok, _ := sess.Get("anything"); ok {
		t.Fatal("expected nothing written to kv")
	}
}

// TestWritePluginKVResult_NoopWithNilSession matches dry-run: kvSess is nil
// whenever opts.Execute is false (step_plugin.go), so this must be a safe
// no-op, not a panic.
func TestWritePluginKVResult_NoopWithNilSession(t *testing.T) {
	t.Parallel()
	pl := &cuetry.RecipeStepPlugin{ID: "duckdb", Action: "query", KVKey: "result"}
	if err := writePluginKVResult(nil, pl, "db1", "anything"); err != nil {
		t.Fatalf("expected no-op, got err: %v", err)
	}
}

// TestWritePluginKVResult_ValueTooLong confirms stepkv's own 65536-byte cap
// (independent of, and 8x larger than, env_from's 8192-byte cap) surfaces as
// a clear step error rather than being silently truncated.
func TestWritePluginKVResult_ValueTooLong(t *testing.T) {
	t.Parallel()
	sess, err := stepkv.Start(0)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	pl := &cuetry.RecipeStepPlugin{ID: "duckdb", Action: "query", KVKey: "result"}
	huge := strings.Repeat("x", 70000)
	err = writePluginKVResult(sess, pl, "db1", huge)
	if err == nil {
		t.Fatal("expected an error for an oversized value")
	}
	if !strings.Contains(err.Error(), "result") {
		t.Fatalf("expected error to name the kv_key, got: %v", err)
	}
}
