package ui

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/plugins"
	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
	"github.com/shareed2k/honey/internal/provider/truenasprovider"
	"github.com/shareed2k/honey/internal/safepath"
	"github.com/shareed2k/honey/internal/truenasshell"
)

type pluginRemoteBridge struct {
	user         string
	record       hosts.Record
	cache        *ClientCache
	reg          hostexec.Registry
	recipeDir    string
	runAs        string
	env          map[string]string
	allowedPaths map[string]string
}

var _ plugins.RemoteBridge = (*pluginRemoteBridge)(nil)

// NewRemoteBridge returns a RemoteBridge for one plugin host invocation.
func NewRemoteBridge(user string, record hosts.Record, cache *ClientCache, reg hostexec.Registry, recipeDir, runAs string, env map[string]string, allowedPaths map[string]string) plugins.RemoteBridge {
	return &pluginRemoteBridge{
		user:         user,
		record:       record,
		cache:        cache,
		reg:          reg,
		recipeDir:    recipeDir,
		runAs:        runAs,
		env:          env,
		allowedPaths: allowedPaths,
	}
}

func (b *pluginRemoteBridge) RemoteExec(ctx context.Context, in apiv1.RemoteExecInput) apiv1.RemoteExecOutput {
	if truenasshell.ShouldUseTrueNASShell(b.record, truenasshell.ConsoleTrueNASAPI) {
		return b.remoteExecTrueNAS(ctx, in)
	}
	return b.remoteExecSSH(in)
}

func (b *pluginRemoteBridge) remoteExecSSH(in apiv1.RemoteExecInput) apiv1.RemoteExecOutput {
	shell := strings.TrimSpace(in.Shell)
	if shell == "" {
		shell = "/bin/sh"
	}
	script := strings.TrimSpace(in.Script)
	runAs := effectivePluginRunAs(in.RunAs, b.runAs)
	inner := shell + " -s"
	var err error
	inner, err = cuetry.ShellExportPrefixForRemote(b.env, inner)
	if err != nil {
		return apiv1.RemoteExecOutput{Failed: true, Error: err.Error()}
	}
	remoteCmd, err := cuetry.WrapRemoteShell(runAs, inner)
	if err != nil {
		return apiv1.RemoteExecOutput{Failed: true, Error: err.Error()}
	}

	var stdout, stderr bytes.Buffer
	for attempt := 1; attempt <= sshTransientOpAttempts; attempt++ {
		client, dialErr := b.dial()
		if dialErr != nil {
			if attempt < sshTransientOpAttempts && IsSSHConnTransientError(dialErr) {
				evictCachedSSHClient(b.cache, b.user, b.record, attempt, nil)
				continue
			}
			return apiv1.RemoteExecOutput{Failed: true, Error: dialErr.Error()}
		}
		runErr := client.RunWithStreams(remoteCmd, strings.NewReader(script), &stdout, &stderr)
		closeSSHIfEphemeral(b.cache, client)
		if runErr == nil {
			return apiv1.RemoteExecOutput{
				ExitCode: 0,
				Stdout:   strings.TrimSpace(stdout.String()),
				Stderr:   strings.TrimSpace(stderr.String()),
				Changed:  true,
			}
		}
		var ee *ssh.ExitError
		if errors.As(runErr, &ee) {
			return apiv1.RemoteExecOutput{
				ExitCode: ee.ExitStatus(),
				Stdout:   strings.TrimSpace(stdout.String()),
				Stderr:   strings.TrimSpace(stderr.String()),
				Failed:   ee.ExitStatus() != 0,
				Changed:  true,
			}
		}
		if attempt < sshTransientOpAttempts && IsSSHConnTransientError(runErr) {
			evictCachedSSHClient(b.cache, b.user, b.record, attempt, nil)
			stdout.Reset()
			stderr.Reset()
			continue
		}
		return apiv1.RemoteExecOutput{Failed: true, Error: runErr.Error(), Stderr: strings.TrimSpace(stderr.String())}
	}
	return apiv1.RemoteExecOutput{Failed: true, Error: "remote_exec failed after retries"}
}

func (b *pluginRemoteBridge) remoteExecTrueNAS(ctx context.Context, in apiv1.RemoteExecInput) apiv1.RemoteExecOutput {
	backend, ok := truenasprovider.BackendByName(b.record.Meta["backend_name"])
	if !ok {
		return apiv1.RemoteExecOutput{Failed: true, Error: "truenas backend not configured"}
	}
	shell := strings.TrimSpace(in.Shell)
	if shell == "" {
		shell = "/bin/sh"
	}
	script := strings.TrimSpace(in.Script)
	runAs := effectivePluginRunAs(in.RunAs, b.runAs)
	b64 := base64.StdEncoding.EncodeToString([]byte(script))
	inner := fmt.Sprintf("echo %s | base64 -d | %s -s", shellSingleQuote(b64), shell)
	var err error
	inner, err = cuetry.ShellExportPrefixForRemote(b.env, inner)
	if err != nil {
		return apiv1.RemoteExecOutput{Failed: true, Error: err.Error()}
	}
	remoteCmd, err := cuetry.WrapRemoteShell(runAs, inner)
	if err != nil {
		return apiv1.RemoteExecOutput{Failed: true, Error: err.Error()}
	}
	out, code, runErr := truenasshell.RunRemoteCommand(ctx, backend, b.record, remoteCmd)
	if runErr != nil {
		return apiv1.RemoteExecOutput{Failed: true, Error: runErr.Error()}
	}
	return apiv1.RemoteExecOutput{
		ExitCode: code,
		Stdout:   strings.TrimSpace(string(out)),
		Changed:  true,
		Failed:   code != 0,
	}
}

func (b *pluginRemoteBridge) RemoteUpload(_ context.Context, in apiv1.RemoteUploadInput) apiv1.RemoteUploadOutput {
	localPath := strings.TrimSpace(in.LocalPath)
	cleanup := func() {}
	if localPath == "" && in.Content != "" {
		tmp, err := os.CreateTemp("", "honey-plugin-ul-*")
		if err != nil {
			return apiv1.RemoteUploadOutput{Failed: true, Error: err.Error()}
		}
		localPath = tmp.Name()
		cleanup = func() { _ = os.Remove(localPath) }
		if _, err := tmp.WriteString(in.Content); err != nil {
			_ = tmp.Close()
			cleanup()
			return apiv1.RemoteUploadOutput{Failed: true, Error: err.Error()}
		}
		_ = tmp.Close()
	}
	defer cleanup()
	for attempt := 1; attempt <= sshTransientOpAttempts; attempt++ {
		client, dialErr := b.dial()
		if dialErr != nil {
			if attempt < sshTransientOpAttempts && IsSSHConnTransientError(dialErr) {
				evictCachedSSHClient(b.cache, b.user, b.record, attempt, nil)
				continue
			}
			return apiv1.RemoteUploadOutput{Failed: true, Error: dialErr.Error()}
		}
		err := client.Upload(localPath, in.RemotePath)
		closeSSHIfEphemeral(b.cache, client)
		if err == nil {
			return apiv1.RemoteUploadOutput{Changed: true}
		}
		if attempt < sshTransientOpAttempts && IsSSHConnTransientError(err) {
			evictCachedSSHClient(b.cache, b.user, b.record, attempt, nil)
			continue
		}
		return apiv1.RemoteUploadOutput{Failed: true, Error: err.Error()}
	}
	return apiv1.RemoteUploadOutput{Failed: true, Error: "remote_upload failed after retries"}
}

func (b *pluginRemoteBridge) RemoteDownload(_ context.Context, in apiv1.RemoteDownloadInput) apiv1.RemoteDownloadOutput {
	maxBytes := in.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 1024 * 1024
	}
	for attempt := 1; attempt <= sshTransientOpAttempts; attempt++ {
		client, dialErr := b.dial()
		if dialErr != nil {
			if attempt < sshTransientOpAttempts && IsSSHConnTransientError(dialErr) {
				evictCachedSSHClient(b.cache, b.user, b.record, attempt, nil)
				continue
			}
			return apiv1.RemoteDownloadOutput{Failed: true, Error: dialErr.Error()}
		}
		content, size, err := readRemoteFile(client, in.RemotePath, maxBytes)
		closeSSHIfEphemeral(b.cache, client)
		if err == nil {
			return apiv1.RemoteDownloadOutput{Content: content, Size: size, Changed: true}
		}
		if attempt < sshTransientOpAttempts && IsSSHConnTransientError(err) {
			evictCachedSSHClient(b.cache, b.user, b.record, attempt, nil)
			continue
		}
		return apiv1.RemoteDownloadOutput{Failed: true, Error: err.Error()}
	}
	return apiv1.RemoteDownloadOutput{Failed: true, Error: "remote_download failed after retries"}
}

func (b *pluginRemoteBridge) RemoteStat(_ context.Context, in apiv1.RemoteStatInput) apiv1.RemoteStatOutput {
	for attempt := 1; attempt <= sshTransientOpAttempts; attempt++ {
		client, dialErr := b.dial()
		if dialErr != nil {
			if attempt < sshTransientOpAttempts && IsSSHConnTransientError(dialErr) {
				evictCachedSSHClient(b.cache, b.user, b.record, attempt, nil)
				continue
			}
			return apiv1.RemoteStatOutput{Failed: true, Error: dialErr.Error()}
		}
		ent, err := client.StatRemote(in.Path)
		closeSSHIfEphemeral(b.cache, client)
		if err == nil {
			mtime := ""
			if !ent.ModifiedAt.IsZero() {
				mtime = ent.ModifiedAt.UTC().Format(time.RFC3339)
			}
			return apiv1.RemoteStatOutput{
				Exists: true,
				IsDir:  ent.IsDir,
				Mode:   ent.Mode,
				Size:   ent.Size,
				MTime:  mtime,
			}
		}
		if isRemoteNotExist(err) {
			return apiv1.RemoteStatOutput{Exists: false}
		}
		if attempt < sshTransientOpAttempts && IsSSHConnTransientError(err) {
			evictCachedSSHClient(b.cache, b.user, b.record, attempt, nil)
			continue
		}
		return apiv1.RemoteStatOutput{Failed: true, Error: err.Error()}
	}
	return apiv1.RemoteStatOutput{Failed: true, Error: "remote_stat failed after retries"}
}

func (b *pluginRemoteBridge) TemplateRender(_ context.Context, in apiv1.TemplateRenderInput) apiv1.TemplateRenderOutput {
	content, err := cuetry.RenderTemplate(cuetry.RenderTemplateOpts{
		Template: in.Template,
		Data:     in.Data,
	})
	if err != nil {
		return apiv1.TemplateRenderOutput{Failed: true, Error: err.Error()}
	}
	return apiv1.TemplateRenderOutput{Content: content}
}

func (b *pluginRemoteBridge) dial() (hostexec.HostClient, error) {
	if b.cache != nil {
		return b.cache.GetOrDial(b.user, b.record)
	}
	if b.reg != nil {
		return b.reg.ForRecord(b.record).Dial(b.user, b.record)
	}
	return nil, fmt.Errorf("no executor registry configured")
}

func readRemoteFile(client hostexec.HostClient, remotePath string, maxBytes int64) (string, int64, error) {
	ent, err := client.StatRemote(remotePath)
	if err != nil {
		return "", 0, err
	}
	if ent.IsDir {
		return "", 0, fmt.Errorf("remote path is a directory")
	}
	if ent.Size > maxBytes {
		return "", ent.Size, fmt.Errorf("remote file exceeds max_bytes (%d > %d)", ent.Size, maxBytes)
	}
	tmp, err := os.CreateTemp("", "honey-plugin-dl-*")
	if err != nil {
		return "", 0, err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := client.Download(remotePath, tmpPath); err != nil {
		return "", 0, err
	}
	data, err := safepath.ReadFile(tmpPath)
	if err != nil {
		return "", 0, err
	}
	return string(data), int64(len(data)), nil
}

func effectivePluginRunAs(stepRunAs, defaultRunAs string) string {
	if s := strings.TrimSpace(stepRunAs); s != "" {
		return s
	}
	return strings.TrimSpace(defaultRunAs)
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, `'`, `'\''`) + "'"
}

func isRemoteNotExist(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such file") || strings.Contains(msg, "not found") || strings.Contains(msg, "does not exist")
}
