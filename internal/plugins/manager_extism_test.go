package plugins

import (
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/shareed2k/honey/internal/config"
	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
	"github.com/shareed2k/honey/internal/stepkv"
)

func loadTestEchoManager(t *testing.T) *Manager {
	t.Helper()
	dir := "testdata"
	cfg := config.PluginsEffective{
		Enabled:     true,
		Directory:   dir,
		MaxMemoryMB: 32,
		TimeoutMS:   30000,
	}
	mgr, err := NewManager(t.Context(), cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	if len(mgr.List()) != 1 || mgr.List()[0].ID != "echo" {
		t.Fatalf("expected echo plugin, got %v", mgr.List())
	}
	return mgr
}

func TestEchoPluginCueTransform(t *testing.T) {
	mgr := loadTestEchoManager(t)
	in := apiv1.CueTransformInput{
		APIVersion: apiv1.APIVersion,
		Cue:        base64.StdEncoding.EncodeToString([]byte("package foo\n")),
	}
	var out apiv1.CueTransformOutput
	if err := mgr.Call(t.Context(), "echo", "cue_transform", in, &out); err != nil {
		t.Fatal(err)
	}
	dec, err := base64.StdEncoding.DecodeString(out.Cue)
	if err != nil {
		t.Fatal(err)
	}
	if string(dec[:len(transformMarker)]) != transformMarker {
		t.Fatalf("got %q", string(dec[:min(40, len(dec))]))
	}
}

const transformMarker = "// honey-echo-transform\n"

func TestEchoPluginResolveSecret(t *testing.T) {
	mgr := loadTestEchoManager(t)
	in := apiv1.ResolveSecretInput{
		APIVersion: apiv1.APIVersion,
		Ref:        "echo:mysecret",
		PluginID:   "echo",
	}
	var out apiv1.ResolveSecretOutput
	if err := mgr.Call(t.Context(), "echo", "resolve_secret", in, &out); err != nil {
		t.Fatal(err)
	}
	if out.Value != "mysecret" {
		t.Fatalf("value=%q", out.Value)
	}
}

func TestEchoPluginExecuteStepDryRun(t *testing.T) {
	mgr := loadTestEchoManager(t)
	in := apiv1.ExecuteStepInput{
		APIVersion: apiv1.APIVersion,
		PluginID:   "echo",
		Action:     "noop",
		Execute:    false,
	}
	var out apiv1.ExecuteStepOutput
	if err := mgr.Call(t.Context(), "echo", "execute_step", in, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Success || out.Stdout != "dry-run" {
		t.Fatalf("out=%+v", out)
	}
}

func TestEchoPluginKVPing(t *testing.T) {
	mgr := loadTestEchoManager(t)
	sess, err := stepkv.Start(0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	ctx := WithKVSession(t.Context(), sess)

	in := apiv1.ExecuteStepInput{
		APIVersion: apiv1.APIVersion,
		PluginID:   "echo",
		Action:     "kv_ping",
		Execute:    true,
	}
	var out apiv1.ExecuteStepOutput
	if err := mgr.Call(ctx, "echo", "execute_step", in, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Success || out.Stdout != "pong" {
		t.Fatalf("out=%+v", out)
	}
	val, found, err := sess.Get("echo-kv-ping")
	if err != nil || !found || val != "pong" {
		t.Fatalf("session get: val=%q found=%v err=%v", val, found, err)
	}
}

func TestEchoPluginConcurrentKVPing(t *testing.T) {
	mgr := loadTestEchoManager(t)
	sess, err := stepkv.Start(0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	ctx := WithKVSession(t.Context(), sess)

	const workers = 12
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			in := apiv1.ExecuteStepInput{
				APIVersion: apiv1.APIVersion,
				PluginID:   "echo",
				Action:     "kv_ping",
				Execute:    true,
			}
			var out apiv1.ExecuteStepOutput
			if err := mgr.Call(ctx, "echo", "execute_step", in, &out); err != nil {
				errs <- err
				return
			}
			if !out.Success || out.Stdout != "pong" {
				errs <- fmt.Errorf("unexpected out: %+v", out)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestEchoPluginHostExec(t *testing.T) {
	mgr := loadTestEchoManager(t)
	in := apiv1.ExecuteStepInput{
		APIVersion: apiv1.APIVersion,
		PluginID:   "echo",
		Action:     "host_exec",
		Execute:    true,
	}
	var out apiv1.ExecuteStepOutput
	if err := mgr.Call(t.Context(), "echo", "execute_step", in, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Success {
		t.Fatalf("success=false err=%q stderr=%q", out.Err, out.Stderr)
	}
	if out.Stdout == "" || !strings.Contains(out.Stdout, "ok") {
		t.Fatalf("stdout=%q", out.Stdout)
	}
}
