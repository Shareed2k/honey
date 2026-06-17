package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
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
type SSHRemoteCmdFunc func(r hosts.Record, kv map[string]string) string

const (
	defaultSSHBatchConcurrency = 32
	maxOutputPerHost           = 6000
	sshTransientOpAttempts     = 3
)

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
type SSHPostHostResultFunc func(ctx context.Context, r hosts.Record, res *HostExecResult)

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
}

// StreamSSHParallel runs the command on records and streams results to out channel.
// It does not close the channel itself.
// StreamSSHParallel ...
func StreamSSHParallel(ctx context.Context, user string, jobs []hosts.Record, kvTunnel bool, remoteCmd SSHRemoteCmdFunc, out chan<- HostExecResult, opts BatchOptions) error {
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

	sem := make(chan struct{}, maxConc)
	var wg sync.WaitGroup
	for i := range jobs {
		wg.Add(1)
		go func(r hosts.Record) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			effUser := strings.TrimSpace(user)
			if effUser == "" {
				if u := strings.TrimSpace(r.Meta["ssh_user"]); u != "" {
					effUser = u
				}
			}

			run := func() HostExecResult {
				if r.Provider == "truenas" && truenasshell.ShouldUseTrueNASShell(r, truenasshell.ConsoleTrueNASAPI) {
					return runOneRemoteTrueNAS(ctx, effUser, r, cache, kvTunnel, remoteCmd, opts.RecipeKV, opts.RecipeScopedKV)
				}
				return RunOneRemoteSSH(effUser, r, cache, kvTunnel, remoteCmd, opts.RecipeKV, opts.RecipeScopedKV)
			}
			outcome := RunHostExecWithRetry(ctx, opts.RetryCfg, run)
			RecordMaxAttempts(opts.AttemptMax, outcome.Attempts)
			op := "exec"
			if r.Provider == "truenas" && truenasshell.ShouldUseTrueNASShell(r, truenasshell.ConsoleTrueNASAPI) {
				op = "truenas"
			}
			observeSSHOperation(opts.Obs, op, hostResultStatus(outcome.Result), outcome.LastAttemptDuration)
			res := outcome.Result
			if opts.Post != nil {
				opts.Post(ctx, r, &res)
			}
			out <- res
		}(jobs[i])
	}
	wg.Wait()
	return nil
}

// ExecuteSSHParallel runs the same remote shell command on every record that has
// PrimaryIP set. Failures on individual hosts do not cancel others.
// It uses DialHoneyClient (golang.org/x/crypto/ssh + ~/.ssh/config) with known_hosts verification.
// ExecuteSSHParallel ...
func ExecuteSSHParallel(user string, recs []hosts.Record, remoteCmdFunc func(hosts.Record) string, maxConc int, reg hostexec.Registry) ([]HostExecResult, error) {
	var jobs []hosts.Record
	for _, r := range recs {
		if strings.TrimSpace(r.PrimaryIP) != "" || (r.Provider == "k8s" && r.Meta["kind"] == "pod") {
			jobs = append(jobs, r)
		}
	}
	if len(jobs) == 0 {
		return []HostExecResult{}, nil
	}

	ch := make(chan HostExecResult, len(jobs))
	go func() {
		defer close(ch)
		wrap := func(r hosts.Record, _ map[string]string) string {
			return remoteCmdFunc(r)
		}
		_ = StreamSSHParallel(context.Background(), user, jobs, false, wrap, ch, BatchOptions{MaxConc: maxConc, Reg: reg})
	}()

	out := make([]HostExecResult, 0, len(jobs))
	for res := range ch {
		out = append(out, res)
	}
	return out, nil
}

func runOneRemoteTrueNAS(ctx context.Context, user string, r hosts.Record, cache *ClientCache, kvTunnel bool, cmd SSHRemoteCmdFunc, recipeKV *RecipeKVCoordinator, recipeScopedKV bool) HostExecResult {
	res := HostExecResult{
		Name:     r.Name,
		IP:       r.PrimaryIP,
		Provider: r.Provider,
	}
	if !truenasshell.ShouldUseTrueNASShell(r, truenasshell.ConsoleTrueNASAPI) {
		res.Success = false
		res.ErrMsg = "truenas api shell not available for this record"
		return res
	}
	b, ok := truenasprovider.BackendByName(r.Meta["backend_name"])
	if !ok {
		res.Success = false
		res.ErrMsg = "truenas backend not configured"
		return res
	}
	var kv map[string]string
	var stopKV func()
	if kvTunnel {
		var kvErr error
		kv, stopKV, kvErr = attachTrueNASKVTunnel(ctx, user, r, kvTunnel, recipeScopedKV, recipeKV, cache)
		if kvErr != nil {
			res.Success = false
			res.ErrMsg = "kv_tunnel: " + kvErr.Error()
			return res
		}
		if stopKV != nil {
			defer stopKV()
		}
	}
	remoteCmd := strings.TrimSpace(cmd(r, kv))
	if remoteCmd == "" {
		res.Success = true
		return res
	}
	out, code, runErr := truenasshell.RunRemoteCommand(ctx, b, r, remoteCmd)
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

// RunOneRemoteSSH ...
func RunOneRemoteSSH(user string, r hosts.Record, cache *ClientCache, kvTunnel bool, cmd SSHRemoteCmdFunc, recipeKV *RecipeKVCoordinator, recipeScopedKV bool) HostExecResult {
	res := HostExecResult{
		Name:     r.Name,
		IP:       r.PrimaryIP,
		Provider: r.Provider,
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

		client, dialErr := cache.GetOrDial(user, r)
		if dialErr != nil {
			if attempt < sshTransientOpAttempts && IsSSHConnTransientError(dialErr) {
				evictCachedSSHClient(cache, user, r, attempt, recipeKV)
				continue
			}
			res.Success = false
			res.ErrMsg = dialErr.Error()
			return res
		}

		var kv map[string]string
		if kvTunnel {
			var kvErr error
			kv, stopKV, kvErr = attachHostKVTunnel(client, user, r, recipeScopedKV, recipeKV)
			if kvErr != nil {
				closeSSHIfEphemeral(cache, client)
				res.Success = false
				res.ErrMsg = "kv_tunnel: " + kvErr.Error()
				return res
			}
		}

		remoteCmd := strings.TrimSpace(cmd(r, kv))
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

		raw, runErr := client.Run(remoteCmd)
		out := strings.TrimSpace(string(raw))
		if len(out) > maxOutputPerHost {
			out = out[:maxOutputPerHost] + "\n…(truncated)"
		}
		res.Output = out

		if runErr != nil {
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
				evictCachedSSHClient(cache, user, r, attempt, recipeKV)
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
func StreamSFTPUploadParallel(ctx context.Context, user string, recs []hosts.Record, localAbs, remotePath string, out chan<- HostExecResult, opts BatchOptions) error {
	localAbs = strings.TrimSpace(localAbs)
	remotePath = strings.TrimSpace(remotePath)
	if localAbs == "" || remotePath == "" {
		return fmt.Errorf("upload: empty local or remote path")
	}
	maxConc := opts.MaxConc
	if maxConc <= 0 {
		maxConc = defaultSSHBatchConcurrency
	}
	var jobs []hosts.Record
	for _, r := range recs {
		if strings.TrimSpace(r.PrimaryIP) != "" || (r.Provider == "k8s" && r.Meta["kind"] == "pod") {
			jobs = append(jobs, r)
		}
	}
	if len(jobs) == 0 {
		return nil
	}
	sem := make(chan struct{}, maxConc)
	var wg sync.WaitGroup
	for i := range jobs {
		wg.Add(1)
		go func(r hosts.Record) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			outcome := RunHostExecWithRetry(ctx, opts.RetryCfg, func() HostExecResult {
				return runOneSFTPUpload(user, r, localAbs, remotePath, opts.Cache)
			})
			RecordMaxAttempts(opts.AttemptMax, outcome.Attempts)
			observeSSHOperation(opts.Obs, "sftp_put", hostResultStatus(outcome.Result), outcome.LastAttemptDuration)
			out <- outcome.Result
		}(jobs[i])
	}
	wg.Wait()
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
	sem := make(chan struct{}, maxConc)
	var wg sync.WaitGroup
	for i := range jobs {
		wg.Add(1)
		go func(j SFTPDownloadJob) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
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
		}(jobs[i])
	}
	wg.Wait()
	return nil
}

// StreamScriptUploadRunParallel uploads a script and executes it on multiple hosts in parallel.
// StreamScriptUploadRunParallel ...
func StreamScriptUploadRunParallel(ctx context.Context, user string, recs []hosts.Record, localAbs, remotePath string, kvTunnel bool, remoteCmd SSHRemoteCmdFunc, out chan<- HostExecResult, opts BatchOptions) error {
	return StreamScriptUploadRunParallelWithOptions(ctx, user, recs, localAbs, remotePath, kvTunnel, remoteCmd, ScriptUploadRunOptions{}, out, opts)
}

// StreamScriptUploadRunParallelWithOptions uploads a script and executes it with optional interpreter/cleanup behavior.
// StreamScriptUploadRunParallelWithOptions ...
func StreamScriptUploadRunParallelWithOptions(ctx context.Context, user string, recs []hosts.Record, localAbs, remotePath string, kvTunnel bool, remoteCmd SSHRemoteCmdFunc, scriptOpts ScriptUploadRunOptions, out chan<- HostExecResult, opts BatchOptions) error {
	cache := opts.Cache
	if cache == nil {
		cache = NewClientCache()
		cache.SetRegistry(opts.Reg)
		defer cache.CloseAll()
	}
	runner, err := newScriptRunner(user, localAbs, remotePath, kvTunnel, remoteCmd, scriptOpts, cache, opts.RecipeKV, opts.RecipeScopedKV)
	if err != nil {
		return err
	}
	var jobs []hosts.Record
	for _, r := range recs {
		if strings.TrimSpace(r.PrimaryIP) != "" || (r.Provider == "k8s" && r.Meta["kind"] == "pod") {
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
func StreamScriptContentRunParallel(ctx context.Context, user string, recs []hosts.Record, scriptContent, fileExtension string, scriptOpts ScriptUploadRunOptions, out chan<- HostExecResult, opts BatchOptions) error {
	cache := opts.Cache
	if cache == nil {
		cache = NewClientCache()
		cache.SetRegistry(opts.Reg)
		defer cache.CloseAll()
	}
	runner, cleanup, err := newScriptContentRunner(user, scriptContent, fileExtension, scriptOpts, cache)
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
func ExecuteScriptContentRunParallel(user string, recs []hosts.Record, scriptContent, fileExtension string, opts ScriptUploadRunOptions, maxConc int, reg hostexec.Registry) ([]HostExecResult, error) {
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
func ExecuteSFTPUploadParallel(user string, recs []hosts.Record, localAbs, remotePath string, maxConc int) ([]HostExecResult, error) {
	var jobs []hosts.Record
	for _, r := range recs {
		if strings.TrimSpace(r.PrimaryIP) != "" || (r.Provider == "k8s" && r.Meta["kind"] == "pod") {
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
			if onProgress != nil {
				st, statErr := os.Stat(localAbs)
				var t int64
				if statErr == nil {
					t = st.Size()
				}
				onProgress(0, t)
			}
			upErr = client.Upload(localAbs, remotePath)
			if onProgress != nil && upErr == nil {
				st, statErr := os.Stat(localAbs)
				if statErr == nil {
					t := st.Size()
					onProgress(t, t)
				}
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
func ExecuteScriptUploadRunParallel(user string, recs []hosts.Record, localAbs, remotePath, remoteCmd string, maxConc int) ([]HostExecResult, error) {
	var jobs []hosts.Record
	for _, r := range recs {
		if strings.TrimSpace(r.PrimaryIP) != "" || (r.Provider == "k8s" && r.Meta["kind"] == "pod") {
			jobs = append(jobs, r)
		}
	}
	if len(jobs) == 0 {
		return []HostExecResult{}, nil
	}

	ch := make(chan HostExecResult, len(jobs))
	go func() {
		defer close(ch)
		wrap := func(_ hosts.Record, _ map[string]string) string { return remoteCmd }
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
