package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/metrics"
	"github.com/shareed2k/honey/internal/provider/truenasprovider"
	"github.com/shareed2k/honey/internal/sshclient"
	"github.com/shareed2k/honey/internal/truenasshell"

	"golang.org/x/crypto/ssh"
)

// SSHRemoteCmdFunc builds the remote shell string. kv is nil when kv_tunnel is disabled; otherwise it contains
// HONEY_KV_URL (reachable from the remote via SSH remote forward) and HONEY_KV_TOKEN for Authorization.
// SSHRemoteCmdFunc ...
type SSHRemoteCmdFunc func(tc TargetContext, kv map[string]string) string

const (
	defaultSSHBatchConcurrency = 32
	maxOutputPerHost           = 6000
	sshTransientOpAttempts     = 3
)

// StreamParallel executes a generic job list concurrently with a bounded waitgroup.
func StreamParallel[T any](jobs []T, maxConc int, worker func(T)) {
	if maxConc <= 0 {
		maxConc = 8
	}
	sem := make(chan struct{}, maxConc)
	var wg sync.WaitGroup
	for i := range jobs {
		wg.Add(1)
		go func(job T) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			worker(job)
		}(jobs[i])
	}
	wg.Wait()
}

func sshTransientBackoff(attempt int) {
	time.Sleep(time.Duration(attempt) * 150 * time.Millisecond)
}

// evictCachedSSHClient removes a dead pooled client (if any) and pauses before redial.
// When recipeKV is non-nil, the host's recipe-scoped KV remote-forward is torn down so the next dial can re-attach.
func evictCachedSSHClient(cache *ClientCache, user string, r hosts.Record, attempt int, recipeKV *RecipeKVCoordinator) {
	if recipeKV != nil {
		recipeKV.InvalidateHost(user, r)
	}
	if cache != nil {
		cache.Evict(user, r)
	}
	sshTransientBackoff(attempt)
}

func closeSSHIfEphemeral(cache *ClientCache, client HostClient) {
	if cache == nil && client != nil {
		_ = client.Close()
	}
}

// SSHPostHostResultFunc runs after each host's main SSH run and before the result is emitted (e.g. CUE step hooks).
// It may set res.HookPhase and res.HookOutput. Hook failures must not change the original step success fields.
// SSHPostHostResultFunc ...
type SSHPostHostResultFunc func(ctx context.Context, tc TargetContext, res *HostExecResult)

// BatchOptions groups the cross-cutting knobs shared by the parallel SSH/SFTP/script
// runners (previously trailing positional parameters). Zero values are valid: a nil
// Cache makes the runner build a short-lived one from Reg; a nil AttemptMax/Obs disables
// the corresponding bookkeeping.
// BatchOptions ...
type BatchOptions struct {
	MaxConc        int
	Cache          *ClientCache
	RecipeKV       *RecipeKVCoordinator
	RecipeScopedKV bool
	Post           SSHPostHostResultFunc
	RetryCfg       cuetry.RecipeStepRetry
	Obs            metrics.Observer
	AttemptMax     *atomic.Int32
	Reg            hostexec.Registry
	// CmdTimeout bounds each per-host remote command; 0 means no timeout. On
	// expiry the SSH session is closed (best-effort kill) and the host result
	// is marked failed/timed-out.
	CmdTimeout time.Duration
	// MaxOutputBytes limits the captured output per host. 0 = default (6000), < 0 = unlimited.
	MaxOutputBytes int
}

func resolveMaxOutputBytes(opts BatchOptions) int {
	if opts.MaxOutputBytes == 0 {
		return maxOutputPerHost
	}
	if opts.MaxOutputBytes < 0 {
		return -1 // unlimited
	}
	return opts.MaxOutputBytes
}

// StreamSSHParallel runs the command on records and streams results to out channel.
// It does not close the channel itself.
// StreamSSHParallel ...
func StreamSSHParallel(ctx context.Context, user string, jobs []TargetContext, kvTunnel bool, remoteCmd SSHRemoteCmdFunc, out chan<- HostExecResult, opts BatchOptions) error {
	maxConc := opts.MaxConc
	if maxConc <= 0 {
		maxConc = defaultSSHBatchConcurrency
	}

	cache := opts.Cache
	// Callers that don't manage their own cache (e.g. the web exec handlers) pass
	// a nil cache and rely on Reg to dial. Build a short-lived cache for this run.
	if cache == nil {
		cache = NewClientCache()
		cache.SetRegistry(opts.Reg)
		defer cache.CloseAll()
	}

	StreamParallel(jobs, maxConc, func(tc TargetContext) {
		// Stop dialing new hosts once the run is cancelled / timed out.
		if ctx.Err() != nil {
			out <- HostExecResult{Name: tc.Record.Name, IP: tc.Record.PrimaryIP, Provider: tc.Record.Provider, Success: false, ErrMsg: "cancelled"}
			return
		}

		effUser := strings.TrimSpace(user)
		if effUser == "" {
			if u := strings.TrimSpace(tc.Record.Meta["ssh_user"]); u != "" {
				effUser = u
			}
		}

		run := func() HostExecResult {
			if tc.Record.Provider == "truenas" && truenasshell.ShouldUseTrueNASShell(tc.Record, truenasshell.ConsoleTrueNASAPI) {
				return runOneRemoteTrueNAS(ctx, effUser, tc, cache, kvTunnel, remoteCmd, opts.RecipeKV, opts.RecipeScopedKV, resolveMaxOutputBytes(opts))
			}
			return RunOneRemoteSSH(ctx, effUser, tc, cache, kvTunnel, remoteCmd, opts.RecipeKV, opts.RecipeScopedKV, opts.CmdTimeout, resolveMaxOutputBytes(opts))
		}
		outcome := RunHostExecWithRetry(ctx, opts.RetryCfg, run)
		RecordMaxAttempts(opts.AttemptMax, outcome.Attempts)
		op := "exec"
		if tc.Record.Provider == "truenas" && truenasshell.ShouldUseTrueNASShell(tc.Record, truenasshell.ConsoleTrueNASAPI) {
			op = "truenas"
		}
		observeSSHOperation(opts.Obs, op, hostResultStatus(outcome.Result), outcome.LastAttemptDuration)
		res := outcome.Result
		if opts.Post != nil {
			opts.Post(ctx, tc, &res)
		}
		out <- res
	})
	return nil
}

// ExecuteSSHParallel runs the same remote shell command on every record that has
// PrimaryIP set. Failures on individual hosts do not cancel others.
// It uses DialHoneyClient (golang.org/x/crypto/ssh + ~/.ssh/config) with known_hosts verification.
// ExecuteSSHParallel ...
func ExecuteSSHParallel(user string, recs []TargetContext, remoteCmdFunc func(hosts.Record) string, maxConc int, reg hostexec.Registry) ([]HostExecResult, error) {
	var jobs []TargetContext
	for _, r := range recs {
		if strings.TrimSpace(r.Record.PrimaryIP) != "" || (r.Record.Provider == "k8s" && r.Record.Meta["kind"] == "pod") {
			jobs = append(jobs, r)
		}
	}
	if len(jobs) == 0 {
		return []HostExecResult{}, nil
	}

	ch := make(chan HostExecResult, len(jobs))
	go func() {
		defer close(ch)
		wrap := func(tc TargetContext, _ map[string]string) string {
			return remoteCmdFunc(tc.Record)
		}
		_ = StreamSSHParallel(context.Background(), user, jobs, false, wrap, ch, BatchOptions{MaxConc: maxConc, Reg: reg})
	}()

	out := make([]HostExecResult, 0, len(jobs))
	for res := range ch {
		out = append(out, res)
	}
	return out, nil
}

func runOneRemoteTrueNAS(ctx context.Context, user string, tc TargetContext, cache *ClientCache, kvTunnel bool, cmd SSHRemoteCmdFunc, recipeKV *RecipeKVCoordinator, recipeScopedKV bool, maxOutputBytes int) HostExecResult {
	res := HostExecResult{
		Name:     tc.Record.Name,
		IP:       tc.Record.PrimaryIP,
		Provider: tc.Record.Provider,
	}
	if !truenasshell.ShouldUseTrueNASShell(tc.Record, truenasshell.ConsoleTrueNASAPI) {
		res.Success = false
		res.ErrMsg = "truenas api shell not available for this record"
		return res
	}
	b, ok := truenasprovider.BackendByName(tc.Record.Meta["backend_name"])
	if !ok {
		res.Success = false
		res.ErrMsg = "truenas backend not configured"
		return res
	}
	var kv map[string]string
	var stopKV func()
	if kvTunnel {
		var kvErr error
		kv, stopKV, kvErr = attachTrueNASKVTunnel(ctx, user, tc.Record, kvTunnel, recipeScopedKV, recipeKV, cache)
		if kvErr != nil {
			res.Success = false
			res.ErrMsg = "kv_tunnel: " + kvErr.Error()
			return res
		}
		if stopKV != nil {
			defer stopKV()
		}
	}
	remoteCmd := strings.TrimSpace(cmd(tc, kv))
	if remoteCmd == "" {
		res.Success = true
		return res
	}
	out, code, runErr := truenasshell.RunRemoteCommand(ctx, b, tc.Record, remoteCmd, maxOutputBytes)
	if runErr != nil {
		res.Success = false
		res.ErrMsg = runErr.Error()
		return res
	}
	res.Output = strings.TrimSpace(string(out))
	res.ExitCode = code
	res.Success = code == 0
	if code != 0 {
		res.ErrMsg = fmt.Sprintf("exit %d", code)
	}
	return res
}

// ctxCommandRunner is the optional capability a HostClient implements when it
// can run a command under a context and abort the in-flight command on cancel/
// timeout (the SSH client closes its session). Clients without it (e.g. k8s,
// which cancels via its own ctx path) fall back to the non-cancellable Run.
type ctxCommandRunner interface {
	RunContext(ctx context.Context, cmd string) ([]byte, error)
}

// RunOneRemoteSSH executes a single remote command on a host with transient retry support.
func RunOneRemoteSSH(ctx context.Context, user string, tc TargetContext, cache *ClientCache, kvTunnel bool, cmd SSHRemoteCmdFunc, recipeKV *RecipeKVCoordinator, recipeScopedKV bool, cmdTimeout time.Duration, maxOutputBytes int) HostExecResult {
	res := HostExecResult{
		Name:     tc.Record.Name,
		IP:       tc.Record.PrimaryIP,
		Provider: tc.Record.Provider,
	}
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

		client, dialErr := cache.GetOrDial(user, tc.Record)
		if dialErr != nil {
			if attempt < sshTransientOpAttempts && IsSSHConnTransientError(dialErr) {
				evictCachedSSHClient(cache, user, tc.Record, attempt, recipeKV)
				continue
			}
			res.Success = false
			res.ErrMsg = dialErr.Error()
			return res
		}

		var kv map[string]string
		if kvTunnel {
			var kvErr error
			kv, stopKV, kvErr = attachHostKVTunnel(client, user, tc.Record, recipeScopedKV, recipeKV)
			if kvErr != nil {
				closeSSHIfEphemeral(cache, client)
				res.Success = false
				res.ErrMsg = "kv_tunnel: " + kvErr.Error()
				return res
			}
		}

		remoteCmd := strings.TrimSpace(cmd(tc, kv))
		wrapped, werr := maybeWrapK8sKVShell(kvTunnel, client, kv, remoteCmd)
		if werr != nil {
			closeSSHIfEphemeral(cache, client)
			res.Success = false
			res.ErrMsg = "kv_tunnel: " + werr.Error()
			return res
		}
		remoteCmd = wrapped
		if remoteCmd == "" {
			closeSSHIfEphemeral(cache, client)
			res.Success = true
			res.ExitCode = 0
			res.Output = ""
			return res
		}

		var raw []byte
		var runErr error
		if rc, ok := client.(ctxCommandRunner); ok {
			// Cancellable path (SSH): closing the session on ctx cancel/timeout
			// aborts the in-flight command.
			if cmdTimeout > 0 {
				cmdCtx, cancel := context.WithTimeout(ctx, cmdTimeout)
				raw, runErr = rc.RunContext(cmdCtx, remoteCmd)
				cancel()
			} else {
				raw, runErr = rc.RunContext(ctx, remoteCmd)
			}
		} else {
			raw, runErr = client.Run(remoteCmd)
		}
		out := strings.TrimSpace(string(raw))
		if maxOutputBytes > 0 && len(out) > maxOutputBytes {
			out = out[:maxOutputBytes] + "\n…(truncated)"
		}
		res.Output = out

		if runErr != nil {
			// Context cancellation / timeout — not retryable, report distinctly.
			if errors.Is(runErr, context.DeadlineExceeded) {
				closeSSHIfEphemeral(cache, client)
				evictCachedSSHClient(cache, user, tc.Record, attempt, recipeKV)
				res.Success = false
				res.ErrMsg = fmt.Sprintf("command timed out after %s", cmdTimeout)
				return res
			}
			if errors.Is(runErr, context.Canceled) {
				closeSSHIfEphemeral(cache, client)
				evictCachedSSHClient(cache, user, tc.Record, attempt, recipeKV)
				res.Success = false
				res.ErrMsg = "cancelled"
				return res
			}
			var ee *ssh.ExitError
			if errors.As(runErr, &ee) {
				closeSSHIfEphemeral(cache, client)
				res.ExitCode = ee.ExitStatus()
				res.Success = false
				if res.ExitCode == 124 {
					if strings.Contains(res.Output, "__HONEY_TIMEOUT_MISSING__") {
						res.ErrMsg = "remote host missing `timeout` command (install coreutils or remove step timeout)"
					} else {
						res.ErrMsg = "command timed out (exit 124)"
					}
				} else if res.ExitCode != 0 {
					res.ErrMsg = fmt.Sprintf("exit %d", res.ExitCode)
				}
				return res
			}
			if attempt < sshTransientOpAttempts && IsSSHConnTransientError(runErr) {
				closeSSHIfEphemeral(cache, client)
				evictCachedSSHClient(cache, user, tc.Record, attempt, recipeKV)
				continue
			}
			closeSSHIfEphemeral(cache, client)
			res.Success = false
			res.ErrMsg = runErr.Error()
			return res
		}
		closeSSHIfEphemeral(cache, client)
		res.Success = true
		res.ExitCode = 0
		return res
	}
	res.Success = false
	res.ErrMsg = "ssh: exceeded transient retry attempts"
	return res
}

// SFTPDownloadJob is one remote→local file copy for a specific host.

// StreamSFTPUploadParallel uploads the same local file to remotePath on each
// record (SFTP over DialHoneyClient). Failures on one host do not cancel others.
// StreamSFTPUploadParallel ...
func StreamSFTPUploadParallel(ctx context.Context, user string, recs []TargetContext, localAbs, remotePath string, out chan<- HostExecResult, opts BatchOptions) error {
	localAbs = filepath.Clean(strings.TrimSpace(localAbs))
	remotePath = strings.TrimSpace(remotePath)
	if localAbs == "" || localAbs == "." || remotePath == "" {
		return fmt.Errorf("upload: empty local or remote path")
	}

	if _, err := os.Stat(localAbs); err != nil {
		return fmt.Errorf("upload: %w", err)
	}

	maxConc := opts.MaxConc
	if maxConc <= 0 {
		maxConc = defaultSSHBatchConcurrency
	}
	var jobs []TargetContext
	for _, r := range recs {
		if strings.TrimSpace(r.Record.PrimaryIP) != "" || (r.Record.Provider == "k8s" && r.Record.Meta["kind"] == "pod") {
			jobs = append(jobs, r)
		}
	}
	if len(jobs) == 0 {
		return nil
	}
	StreamParallel(jobs, maxConc, func(tc TargetContext) {
		outcome := RunHostExecWithRetry(ctx, opts.RetryCfg, func() HostExecResult {
			return runOneSFTPUpload(user, tc.Record, localAbs, remotePath, opts.Cache)
		})
		RecordMaxAttempts(opts.AttemptMax, outcome.Attempts)
		observeSSHOperation(opts.Obs, "sftp_put", hostResultStatus(outcome.Result), outcome.LastAttemptDuration)
		out <- outcome.Result
	})
	return nil
}

// StreamSFTPDownloadParallel downloads files from multiple hosts in parallel.
// StreamSFTPDownloadParallel ...
func StreamSFTPDownloadParallel(ctx context.Context, user string, jobs []SFTPDownloadJob, out chan<- HostExecResult, opts BatchOptions) error {
	maxConc := opts.MaxConc
	if maxConc <= 0 {
		maxConc = defaultSSHBatchConcurrency
	}
	if len(jobs) == 0 {
		return nil
	}
	StreamParallel(jobs, maxConc, func(j SFTPDownloadJob) {
		if strings.TrimSpace(j.Record.PrimaryIP) == "" && (j.Record.Provider != "k8s" || j.Record.Meta["kind"] != "pod") {
			out <- HostExecResult{Name: j.Record.Name, Provider: j.Record.Provider, Success: false, ErrMsg: "missing PrimaryIP"}
			return
		}
		outcome := RunHostExecWithRetry(ctx, opts.RetryCfg, func() HostExecResult {
			return runOneSFTPDownload(user, j, opts.Cache)
		})
		RecordMaxAttempts(opts.AttemptMax, outcome.Attempts)
		observeSSHOperation(opts.Obs, "sftp_get", hostResultStatus(outcome.Result), outcome.LastAttemptDuration)
		out <- outcome.Result
	})
	return nil
}

// StreamScriptUploadRunParallel uploads a script and executes it on multiple hosts in parallel.
// StreamScriptUploadRunParallel ...
func StreamScriptUploadRunParallel(ctx context.Context, user string, recs []TargetContext, localAbs, remotePath string, kvTunnel bool, remoteCmd SSHRemoteCmdFunc, out chan<- HostExecResult, opts BatchOptions) error {
	return StreamScriptUploadRunParallelWithOptions(ctx, user, recs, localAbs, remotePath, kvTunnel, remoteCmd, ScriptUploadRunOptions{}, out, opts)
}

// StreamScriptUploadRunParallelWithOptions uploads a script and executes it with optional interpreter/cleanup behavior.
// StreamScriptUploadRunParallelWithOptions ...
func StreamScriptUploadRunParallelWithOptions(ctx context.Context, user string, recs []TargetContext, localAbs, remotePath string, kvTunnel bool, remoteCmd SSHRemoteCmdFunc, scriptOpts ScriptUploadRunOptions, out chan<- HostExecResult, opts BatchOptions) error {
	cache := opts.Cache
	if cache == nil {
		cache = NewClientCache()
		cache.SetRegistry(opts.Reg)
		defer cache.CloseAll()
	}
	runner, err := newScriptRunner(user, localAbs, remotePath, kvTunnel, remoteCmd, scriptOpts, cache, opts.RecipeKV, opts.RecipeScopedKV, resolveMaxOutputBytes(opts))
	if err != nil {
		return err
	}
	runner.cmdTimeout = opts.CmdTimeout
	var jobs []TargetContext
	for _, r := range recs {
		if strings.TrimSpace(r.Record.PrimaryIP) != "" || (r.Record.Provider == "k8s" && r.Record.Meta["kind"] == "pod") {
			jobs = append(jobs, r)
		}
	}
	if len(jobs) == 0 {
		return nil
	}
	runner.stream(ctx, jobs, opts.MaxConc, out, opts.Post, opts.RetryCfg, opts.Obs, opts.AttemptMax)
	return nil
}

// StreamScriptContentRunParallel writes scriptContent to a local temp file, uploads it to each host,
// runs it using Rundeck-style script-file semantics, and removes the local temp file afterwards.
// StreamScriptContentRunParallel ...
func StreamScriptContentRunParallel(ctx context.Context, user string, recs []TargetContext, scriptContent, fileExtension string, scriptOpts ScriptUploadRunOptions, out chan<- HostExecResult, opts BatchOptions) error {
	cache := opts.Cache
	if cache == nil {
		cache = NewClientCache()
		cache.SetRegistry(opts.Reg)
		defer cache.CloseAll()
	}
	runner, cleanup, err := newScriptContentRunner(user, scriptContent, fileExtension, scriptOpts, cache, resolveMaxOutputBytes(opts))
	if err != nil {
		return err
	}
	defer cleanup()
	runner.stream(ctx, recs, opts.MaxConc, out, nil, opts.RetryCfg, opts.Obs, opts.AttemptMax)
	return nil
}

// ExecuteScriptContentRunParallel writes scriptContent to a local temp file, uploads it to each host,
// runs it using Rundeck-style script-file semantics in parallel, and returns results synchronously.
// ExecuteScriptContentRunParallel ...
func ExecuteScriptContentRunParallel(user string, recs []TargetContext, scriptContent, fileExtension string, opts ScriptUploadRunOptions, maxConc int, reg hostexec.Registry) ([]HostExecResult, error) {
	if len(recs) == 0 {
		return []HostExecResult{}, nil
	}
	ch := make(chan HostExecResult, len(recs))
	go func() {
		defer close(ch)
		_ = StreamScriptContentRunParallel(context.Background(), user, recs, scriptContent, fileExtension, opts, ch, BatchOptions{MaxConc: maxConc, Reg: reg})
	}()
	out := make([]HostExecResult, 0, len(recs))
	for res := range ch {
		out = append(out, res)
	}
	return out, nil
}

// ExecuteSFTPUploadParallel executes an SFTP upload in parallel across multiple hosts and returns results synchronously.
// ExecuteSFTPUploadParallel ...
func ExecuteSFTPUploadParallel(user string, recs []TargetContext, localAbs, remotePath string, maxConc int) ([]HostExecResult, error) {
	var jobs []TargetContext
	for _, r := range recs {
		if strings.TrimSpace(r.Record.PrimaryIP) != "" || (r.Record.Provider == "k8s" && r.Record.Meta["kind"] == "pod") {
			jobs = append(jobs, r)
		}
	}
	if len(jobs) == 0 {
		return []HostExecResult{}, nil
	}

	ch := make(chan HostExecResult, len(jobs))
	go func() {
		defer close(ch)
		_ = StreamSFTPUploadParallel(context.Background(), user, recs, localAbs, remotePath, ch, BatchOptions{MaxConc: maxConc})
	}()

	out := make([]HostExecResult, 0, len(jobs))
	for res := range ch {
		out = append(out, res)
	}
	return out, nil
}

// ExecuteSFTPDownloadParallel runs each download job (possibly different local
// paths per host) in parallel.
// ExecuteSFTPDownloadParallel ...
func ExecuteSFTPDownloadParallel(user string, jobs []SFTPDownloadJob, maxConc int) ([]HostExecResult, error) {
	if len(jobs) == 0 {
		return []HostExecResult{}, nil
	}

	ch := make(chan HostExecResult, len(jobs))
	go func() {
		defer close(ch)
		_ = StreamSFTPDownloadParallel(context.Background(), user, jobs, ch, BatchOptions{MaxConc: maxConc})
	}()

	out := make([]HostExecResult, 0, len(jobs))
	for res := range ch {
		out = append(out, res)
	}
	return out, nil
}

// RunOneSFTPUploadWithProgress uploads one local file to remotePath on r, like runOneSFTPUpload.
// onProgress is optional; it receives cumulative bytes written toward the remote and the local file
// size. Live updates are emitted for *sshclient.HoneyClient (SFTP); other executors only report start/end.
// RunOneSFTPUploadWithProgress ...
func RunOneSFTPUploadWithProgress(user string, r hosts.Record, localAbs, remotePath string, cache *ClientCache, onProgress func(written, total int64)) HostExecResult {
	res := HostExecResult{Name: r.Name, IP: r.PrimaryIP, Provider: r.Provider}
	localAbs = filepath.Clean(strings.TrimSpace(localAbs))
	if localAbs == "" || localAbs == "." {
		res.Success = false
		res.ErrMsg = "upload: empty local path"
		return res
	}
	for attempt := 1; attempt <= sshTransientOpAttempts; attempt++ {
		client, dialErr := cache.GetOrDial(user, r)
		if dialErr != nil {
			if attempt < sshTransientOpAttempts && IsSSHConnTransientError(dialErr) {
				evictCachedSSHClient(cache, user, r, attempt, nil)
				continue
			}
			res.Success = false
			res.ErrMsg = dialErr.Error()
			return res
		}
		var upErr error
		if hc, ok := client.(*sshclient.HoneyClient); ok && onProgress != nil {
			upErr = hc.UploadWithProgress(localAbs, remotePath, onProgress)
		} else {
			var t int64
			if onProgress != nil {
				if st, statErr := os.Stat(localAbs); statErr == nil {
					t = st.Size()
				}
				onProgress(0, t)
			}
			upErr = client.Upload(localAbs, remotePath)
			if onProgress != nil && upErr == nil {
				onProgress(t, t)
			}
		}
		if upErr != nil {
			if attempt < sshTransientOpAttempts && IsSSHConnTransientError(upErr) {
				closeSSHIfEphemeral(cache, client)
				evictCachedSSHClient(cache, user, r, attempt, nil)
				continue
			}
			closeSSHIfEphemeral(cache, client)
			res.Success = false
			res.ErrMsg = upErr.Error()
			return res
		}
		closeSSHIfEphemeral(cache, client)
		res.Success = true
		res.ExitCode = 0
		res.Output = "put " + localAbs + " → " + remotePath
		return res
	}
	res.Success = false
	res.ErrMsg = "sftp put: exceeded transient retry attempts"
	return res
}

func runOneSFTPUpload(user string, r hosts.Record, localAbs, remotePath string, cache *ClientCache) HostExecResult {
	return RunOneSFTPUploadWithProgress(user, r, localAbs, remotePath, cache, nil)
}

// ExecuteScriptUploadRunParallel uploads localAbs to remotePath on each host over SFTP,
// then runs remoteCmd on the same SSH connection (one session per host per step).
// ExecuteScriptUploadRunParallel ...
func ExecuteScriptUploadRunParallel(user string, recs []TargetContext, localAbs, remotePath, remoteCmd string, maxConc int) ([]HostExecResult, error) {
	var jobs []TargetContext
	for _, r := range recs {
		if strings.TrimSpace(r.Record.PrimaryIP) != "" || (r.Record.Provider == "k8s" && r.Record.Meta["kind"] == "pod") {
			jobs = append(jobs, r)
		}
	}
	if len(jobs) == 0 {
		return []HostExecResult{}, nil
	}

	ch := make(chan HostExecResult, len(jobs))
	go func() {
		defer close(ch)
		wrap := func(_ TargetContext, _ map[string]string) string { return remoteCmd }
		_ = StreamScriptUploadRunParallel(context.Background(), user, recs, localAbs, remotePath, false, wrap, ch, BatchOptions{MaxConc: maxConc})
	}()

	out := make([]HostExecResult, 0, len(jobs))
	for res := range ch {
		out = append(out, res)
	}
	return out, nil
}

func runOneSFTPDownload(user string, j SFTPDownloadJob, cache *ClientCache) HostExecResult {
	r := j.Record
	res := HostExecResult{Name: r.Name, IP: r.PrimaryIP, Provider: r.Provider}
	for attempt := 1; attempt <= sshTransientOpAttempts; attempt++ {
		client, dialErr := cache.GetOrDial(user, r)
		if dialErr != nil {
			if attempt < sshTransientOpAttempts && IsSSHConnTransientError(dialErr) {
				evictCachedSSHClient(cache, user, r, attempt, nil)
				continue
			}
			res.Success = false
			res.ErrMsg = dialErr.Error()
			return res
		}
		dlErr := client.Download(j.RemoteAbs, j.LocalAbs)
		if dlErr != nil {
			if attempt < sshTransientOpAttempts && IsSSHConnTransientError(dlErr) {
				closeSSHIfEphemeral(cache, client)
				evictCachedSSHClient(cache, user, r, attempt, nil)
				continue
			}
			closeSSHIfEphemeral(cache, client)
			res.Success = false
			res.ErrMsg = dlErr.Error()
			return res
		}
		closeSSHIfEphemeral(cache, client)
		res.Success = true
		res.ExitCode = 0
		res.Output = "get " + j.RemoteAbs + " → " + j.LocalAbs
		return res
	}
	res.Success = false
	res.ErrMsg = "sftp get: exceeded transient retry attempts"
	return res
}

// SortHostExecForUI orders failures first, then host name (case-insensitive).
// SortHostExecForUI ...
func SortHostExecForUI(s []HostExecResult) []HostExecResult {
	if len(s) < 2 {
		return s
	}
	cp := append([]HostExecResult(nil), s...)
	sort.Slice(cp, func(i, j int) bool {
		if cp[i].Success != cp[j].Success {
			return !cp[i].Success && cp[j].Success
		}
		return strings.ToLower(cp[i].Name) < strings.ToLower(cp[j].Name)
	})
	return cp
}
