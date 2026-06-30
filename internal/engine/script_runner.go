package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/metrics"
	"golang.org/x/crypto/ssh"
)

// ScriptUploadRunOptions controls upload/chmod/execute script runs.
// ScriptUploadRunOptions ...
type ScriptUploadRunOptions struct {
	ScriptInterpreter     string
	InterpreterArgsQuoted bool
	RemoveRemoteFile      bool
	// ScriptArgs are positional arguments passed to the script (Rundeck-style),
	// shell-quoted and appended after the interpreter/path.
	ScriptArgs []string
	// RunAs wraps the run step in sudo for that user (empty = run as the SSH user).
	RunAs string
}

type scriptRunner struct {
	user         string
	localAbs     string
	remotePath   string
	cmd          SSHRemoteCmdFunc
	opts         ScriptUploadRunOptions
	cache        *ClientCache
	kvTunnel     bool
	recipeKV     *RecipeKVCoordinator
	recipeScoped bool
}

func newScriptRunner(user, localAbs, remotePath string, kvTunnel bool, cmd SSHRemoteCmdFunc, opts ScriptUploadRunOptions, cache *ClientCache, recipeKV *RecipeKVCoordinator, recipeScoped bool) (*scriptRunner, error) {
	localAbs = strings.TrimSpace(localAbs)
	remotePath = strings.TrimSpace(remotePath)
	if localAbs == "" || remotePath == "" {
		return nil, fmt.Errorf("script step: empty local or remote path")
	}
	if cmd == nil {
		return nil, fmt.Errorf("script step: empty remote command")
	}
	return &scriptRunner{
		user:         user,
		localAbs:     localAbs,
		remotePath:   remotePath,
		cmd:          cmd,
		opts:         opts,
		cache:        cache,
		kvTunnel:     kvTunnel,
		recipeKV:     recipeKV,
		recipeScoped: recipeScoped,
	}, nil
}

func newScriptContentRunner(user, scriptContent, fileExtension string, opts ScriptUploadRunOptions, cache *ClientCache) (*scriptRunner, func(), error) {
	localAbs, remotePath, cleanup, err := prepareScriptContentFile(scriptContent, fileExtension)
	if err != nil {
		return nil, nil, err
	}
	remoteCmd, err := buildScriptInvocationCommand(remotePath, opts)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	cmdFunc := func(_ TargetContext, _ map[string]string) string { return remoteCmd }
	runner, err := newScriptRunner(user, localAbs, remotePath, false, cmdFunc, opts, cache, nil, false)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return runner, cleanup, nil
}

func (sr *scriptRunner) stream(ctx context.Context, recs []TargetContext, maxConc int, out chan<- HostExecResult, post SSHPostHostResultFunc, retryCfg cuetry.RecipeStepRetry, obs metrics.Observer, attemptMax *atomic.Int32) {
	if maxConc <= 0 {
		maxConc = defaultSSHBatchConcurrency
	}
	sem := make(chan struct{}, maxConc)
	var wg sync.WaitGroup
	for i := range recs {
		wg.Add(1)
		go func(tc TargetContext) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			outcome := RunHostExecWithRetry(ctx, retryCfg, func() HostExecResult {
				return sr.runHost(tc)
			})
			RecordMaxAttempts(attemptMax, outcome.Attempts)
			observeSSHOperation(obs, "script", hostResultStatus(outcome.Result), outcome.LastAttemptDuration)
			res := outcome.Result
			if post != nil {
				post(ctx, tc, &res)
			}
			out <- res
		}(recs[i])
	}
	wg.Wait()
}

func (sr *scriptRunner) runHost(tc TargetContext) HostExecResult {
	res := HostExecResult{Name: tc.Record.Name, IP: tc.Record.PrimaryIP, Provider: tc.Record.Provider}
	var stopKV func()
	defer func() {
		if stopKV != nil {
			stopKV()
		}
	}()

	for attempt := 1; attempt <= sshTransientOpAttempts; attempt++ {
		if stopKV != nil {
			stopKV()
			stopKV = nil
		}

		client, dialErr := sr.cache.GetOrDial(sr.user, tc.Record)
		if dialErr != nil {
			if attempt < sshTransientOpAttempts && IsSSHConnTransientError(dialErr) {
				evictCachedSSHClient(sr.cache, sr.user, tc.Record, attempt, sr.recipeKV)
				continue
			}
			res.Success = false
			res.ErrMsg = dialErr.Error()
			return res
		}

		if upErr := client.Upload(sr.localAbs, sr.remotePath); upErr != nil {
			if attempt < sshTransientOpAttempts && IsSSHConnTransientError(upErr) {
				closeSSHIfEphemeral(sr.cache, client)
				evictCachedSSHClient(sr.cache, sr.user, tc.Record, attempt, sr.recipeKV)
				continue
			}
			closeSSHIfEphemeral(sr.cache, client)
			res.Success = false
			res.ErrMsg = "upload: " + upErr.Error()
			return res
		}

		cleanupRemote := func() {
			if sr.opts.RemoveRemoteFile {
				_, _ = client.Run("rm -f " + shellQuote(sr.remotePath))
			}
		}

		if _, chmodErr := client.Run("chmod +x " + shellQuote(sr.remotePath)); chmodErr != nil {
			cleanupRemote()
			closeSSHIfEphemeral(sr.cache, client)
			res.Success = false
			res.ErrMsg = "chmod: " + chmodErr.Error()
			return res
		}

		var kv map[string]string
		if sr.kvTunnel {
			var kvErr error
			kv, stopKV, kvErr = attachHostKVTunnel(client, sr.user, tc.Record, sr.recipeScoped, sr.recipeKV)
			if kvErr != nil {
				cleanupRemote()
				closeSSHIfEphemeral(sr.cache, client)
				res.Success = false
				res.ErrMsg = "kv_tunnel: " + kvErr.Error()
				return res
			}
		}

		remoteCmd := strings.TrimSpace(sr.cmd(tc, kv))
		wrapped, werr := maybeWrapK8sKVShell(sr.kvTunnel, client, kv, remoteCmd)
		if werr != nil {
			cleanupRemote()
			closeSSHIfEphemeral(sr.cache, client)
			res.Success = false
			res.ErrMsg = "kv_tunnel: " + werr.Error()
			return res
		}
		remoteCmd = wrapped
		if remoteCmd == "" {
			cleanupRemote()
			closeSSHIfEphemeral(sr.cache, client)
			res.Success = true
			res.ExitCode = 0
			res.Output = "script put -> " + sr.remotePath
			return res
		}

		raw, runErr := client.Run(remoteCmd)
		out := strings.TrimSpace(string(raw))
		if len(out) > maxOutputPerHost {
			out = out[:maxOutputPerHost] + "\n...(truncated)"
		}
		res.Output = "script put -> " + sr.remotePath + "\n" + out

		if runErr != nil {
			var ee *ssh.ExitError
			if errors.As(runErr, &ee) {
				cleanupRemote()
				closeSSHIfEphemeral(sr.cache, client)
				res.ExitCode = ee.ExitStatus()
				res.Success = false
				if res.ExitCode != 0 {
					res.ErrMsg = fmt.Sprintf("run: exit %d", res.ExitCode)
				}
				return res
			}
			if attempt < sshTransientOpAttempts && IsSSHConnTransientError(runErr) {
				cleanupRemote()
				closeSSHIfEphemeral(sr.cache, client)
				evictCachedSSHClient(sr.cache, sr.user, tc.Record, attempt, sr.recipeKV)
				continue
			}
			cleanupRemote()
			closeSSHIfEphemeral(sr.cache, client)
			res.Success = false
			res.ErrMsg = "run: " + runErr.Error()
			return res
		}
		cleanupRemote()
		closeSSHIfEphemeral(sr.cache, client)
		res.Success = true
		res.ExitCode = 0
		return res
	}
	res.Success = false
	res.ErrMsg = "script step: exceeded transient retry attempts"
	return res
}

func prepareScriptContentFile(scriptContent, fileExtension string) (string, string, func(), error) {
	if strings.TrimSpace(scriptContent) == "" {
		return "", "", nil, fmt.Errorf("script content empty")
	}
	ext, err := normalizeScriptFileExtension(fileExtension)
	if err != nil {
		return "", "", nil, err
	}
	f, err := os.CreateTemp("", "honey-web-exec-*"+ext)
	if err != nil {
		return "", "", nil, fmt.Errorf("create temp script: %w", err)
	}
	cleanup := func() { _ = os.Remove(f.Name()) }
	if _, err := f.WriteString(scriptContent); err != nil {
		_ = f.Close()
		cleanup()
		return "", "", nil, fmt.Errorf("write temp script: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", "", nil, fmt.Errorf("close temp script: %w", err)
	}
	return f.Name(), "/tmp/" + filepath.Base(f.Name()), cleanup, nil
}

func normalizeScriptFileExtension(fileExtension string) (string, error) {
	ext := strings.TrimSpace(fileExtension)
	if ext == "" {
		return ".sh", nil
	}
	if strings.ContainsAny(ext, `/\`) {
		return "", fmt.Errorf("file extension must not contain path separators")
	}
	if len(ext) > 32 {
		return "", fmt.Errorf("file extension too long")
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return ext, nil
}

func buildScriptInvocationCommand(remotePath string, opts ScriptUploadRunOptions) (string, error) {
	rp := strings.TrimSpace(remotePath)
	if rp == "" {
		return "", fmt.Errorf("empty script remote path")
	}
	quotedPath := shellQuote(rp)
	interp := strings.TrimSpace(opts.ScriptInterpreter)
	var invocation string
	switch {
	case interp == "":
		invocation = quotedPath
	case strings.Contains(interp, "${scriptfile}"):
		invocation = strings.ReplaceAll(interp, "${scriptfile}", quotedPath)
	case opts.InterpreterArgsQuoted:
		invocation = interp + " " + shellQuote(rp)
	default:
		invocation = interp + " " + quotedPath
	}
	for _, a := range opts.ScriptArgs {
		invocation += " " + shellQuote(a)
	}
	if strings.TrimSpace(opts.RunAs) != "" {
		return cuetry.WrapRemoteShell(opts.RunAs, invocation)
	}
	return invocation, nil
}
