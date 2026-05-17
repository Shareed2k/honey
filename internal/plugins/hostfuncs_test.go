package plugins

import (
	"slices"
	"strings"
	"testing"

	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
	"github.com/shareed2k/honey/internal/stepkv"
)

func TestHostFunctions_hostExecGated(t *testing.T) {
	t.Parallel()
	none := hostFunctionNames(Manifest{ID: "x", AllowedEnv: []string{"HOME"}})
	if slices.Contains(none, "host_exec") {
		t.Fatal("host_exec present without allow_host_exec")
	}
	if !slices.Contains(none, "log_info") {
		t.Fatal("expected log_info")
	}
	allowed := hostFunctionNames(Manifest{ID: "x", AllowHostExec: true})
	if !slices.Contains(allowed, "host_exec") {
		t.Fatal("expected host_exec when allow_host_exec is true")
	}
}

func TestHostFunctions_kvGated(t *testing.T) {
	t.Parallel()
	none := hostFunctionNames(Manifest{ID: "x"})
	if slices.Contains(none, "kv") {
		t.Fatal("kv present without allow_kv")
	}
	allowed := hostFunctionNames(Manifest{ID: "x", AllowKV: true})
	if !slices.Contains(allowed, "kv") {
		t.Fatal("expected kv when allow_kv is true")
	}
}

func TestRunKV_getPutDelete(t *testing.T) {
	t.Parallel()
	s, err := stepkv.Start(0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := WithKVSession(t.Context(), s)

	out := RunKVForTest(ctx, apiv1.KVInput{Op: "put", Key: "k", Value: "v"})
	if out.Error != "" {
		t.Fatalf("put: %s", out.Error)
	}
	out = RunKVForTest(ctx, apiv1.KVInput{Op: "get", Key: "k"})
	if !out.Found || out.Value != "v" || out.Error != "" {
		t.Fatalf("get: %+v", out)
	}
	out = RunKVForTest(ctx, apiv1.KVInput{Op: "delete", Key: "k"})
	if out.Error != "" {
		t.Fatalf("delete: %s", out.Error)
	}
	out = RunKVForTest(ctx, apiv1.KVInput{Op: "get", Key: "k"})
	if out.Found || out.Error != "" {
		t.Fatalf("get after delete: %+v", out)
	}
}

func TestRunKV_noSession(t *testing.T) {
	t.Parallel()
	out := RunKVForTest(t.Context(), apiv1.KVInput{Op: "get", Key: "k"})
	if out.Error == "" || !strings.Contains(out.Error, "not available") {
		t.Fatalf("got %+v", out)
	}
}

func TestRunHostExec_echo(t *testing.T) {
	t.Parallel()
	out := runHostExec(t.Context(), apiv1.HostExecInput{
		Argv:      []string{"echo", "ok"},
		TimeoutMS: 5000,
	}, 5000)
	if out.ExitCode != 0 {
		t.Fatalf("exit=%d err=%q stderr=%q", out.ExitCode, out.Error, out.Stderr)
	}
	if !strings.Contains(out.Stdout, "ok") {
		t.Fatalf("stdout=%q", out.Stdout)
	}
}
