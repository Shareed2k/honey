package plugins

import (
	"context"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/hosts"

	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
)

type fakeRemoteBridge struct {
	execCalled bool
	lastExec   apiv1.RemoteExecInput
}

func (f *fakeRemoteBridge) RemoteExec(_ context.Context, in apiv1.RemoteExecInput) apiv1.RemoteExecOutput {
	f.execCalled = true
	f.lastExec = in
	return apiv1.RemoteExecOutput{ExitCode: 0, Stdout: "ok", Changed: true}
}

func (f *fakeRemoteBridge) RemoteUpload(context.Context, apiv1.RemoteUploadInput) apiv1.RemoteUploadOutput {
	return apiv1.RemoteUploadOutput{Changed: true}
}

func (f *fakeRemoteBridge) RemoteDownload(context.Context, apiv1.RemoteDownloadInput) apiv1.RemoteDownloadOutput {
	return apiv1.RemoteDownloadOutput{}
}

func (f *fakeRemoteBridge) RemoteStat(context.Context, apiv1.RemoteStatInput) apiv1.RemoteStatOutput {
	return apiv1.RemoteStatOutput{Exists: true}
}

func (f *fakeRemoteBridge) TemplateRender(_ context.Context, in apiv1.TemplateRenderInput) apiv1.TemplateRenderOutput {
	return apiv1.TemplateRenderOutput{Content: in.Template}
}

func TestHostFunctions_remoteExecGated(t *testing.T) {
	t.Parallel()
	none := hostFunctionNames(Manifest{ID: "x"})
	if slicesContains(none, "remote_exec") {
		t.Fatal("remote_exec present without allow_remote_exec")
	}
	with := hostFunctionNames(Manifest{ID: "x", AllowRemoteExec: true})
	if !slicesContains(with, "remote_exec") {
		t.Fatal("expected remote_exec when allow_remote_exec is true")
	}
}

func TestHostFunctions_sftpGated(t *testing.T) {
	t.Parallel()
	none := hostFunctionNames(Manifest{ID: "x"})
	for _, name := range []string{"remote_upload", "remote_download", "remote_stat"} {
		if slicesContains(none, name) {
			t.Fatalf("%s present without allow_sftp", name)
		}
	}
	with := hostFunctionNames(Manifest{ID: "x", AllowSFTP: true})
	for _, name := range []string{"remote_upload", "remote_download", "remote_stat"} {
		if !slicesContains(with, name) {
			t.Fatalf("expected %s when allow_sftp is true", name)
		}
	}
}

func TestRunRemoteExec_dryRun(t *testing.T) {
	t.Parallel()
	ctx := WithHostRunContext(t.Context(), &HostRunContext{
		Execute: false,
		Record:  hosts.Record{Name: "web1"},
	})
	out := RunRemoteExecForTest(ctx, apiv1.RemoteExecInput{Shell: "/bin/bash", Script: "echo hi"})
	if !out.Changed || out.Failed || !strings.Contains(out.Stdout, "web1") {
		t.Fatalf("got %+v", out)
	}
}

func TestRunRemoteExec_execute(t *testing.T) {
	t.Parallel()
	bridge := &fakeRemoteBridge{}
	ctx := WithHostRunContext(t.Context(), &HostRunContext{
		Execute: true,
		Record:  hosts.Record{Name: "web1"},
		Bridge:  bridge,
	})
	out := RunRemoteExecForTest(ctx, apiv1.RemoteExecInput{Shell: "/bin/bash", Script: "echo hi"})
	if !bridge.execCalled || out.Stdout != "ok" {
		t.Fatalf("bridge=%v out=%+v", bridge, out)
	}
}

func TestRunRemoteExec_noContext(t *testing.T) {
	t.Parallel()
	out := RunRemoteExecForTest(t.Context(), apiv1.RemoteExecInput{Script: "x"})
	if !out.Failed || out.Error == "" {
		t.Fatalf("got %+v", out)
	}
}

func slicesContains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
