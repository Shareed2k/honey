package ui

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shareed2k/honey/internal/hosts"

	"golang.org/x/crypto/ssh"
)

const (
	defaultSSHBatchConcurrency = 32
	maxOutputPerHost           = 6000
	sshTransientOpAttempts     = 3
)

func sshTransientBackoff(attempt int) {
	time.Sleep(time.Duration(attempt) * 150 * time.Millisecond)
}

// evictCachedSSHClient removes a dead pooled client (if any) and pauses before redial.
func evictCachedSSHClient(cache *ClientCache, user string, r hosts.Record, attempt int) {
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

// HostExecResult is the outcome of one non-interactive ssh run.
type HostExecResult struct {
	Name     string
	IP       string
	Provider string
	Success  bool
	ExitCode int
	Output   string
	ErrMsg   string // transport / spawn failure (not remote stderr)
}

// StreamSSHParallel runs the command on records and streams results to out channel.
// It does not close the channel itself.
func StreamSSHParallel(user string, jobs []hosts.Record, remoteCmdFunc func(hosts.Record) string, maxConc int, out chan<- HostExecResult, cache *ClientCache) error {
	if maxConc <= 0 {
		maxConc = defaultSSHBatchConcurrency
	}

	sem := make(chan struct{}, maxConc)
	var wg sync.WaitGroup
	for i := range jobs {
		wg.Add(1)
		go func(r hosts.Record) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			cmd := remoteCmdFunc(r)
			if strings.TrimSpace(cmd) == "" {
				out <- HostExecResult{Name: r.Name, Provider: r.Provider, Success: true, Output: ""}
				return
			}
			out <- runOneRemoteSSH(user, r, cmd, cache)
		}(jobs[i])
	}
	wg.Wait()
	return nil
}

// ExecuteSSHParallel runs the same remote shell command on every record that has
// PrimaryIP set. Failures on individual hosts do not cancel others.
// It uses DialHoneyClient (golang.org/x/crypto/ssh + ~/.ssh/config) with known_hosts verification.
func ExecuteSSHParallel(user string, recs []hosts.Record, remoteCmdFunc func(hosts.Record) string, maxConc int) ([]HostExecResult, error) {
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
		_ = StreamSSHParallel(user, jobs, remoteCmdFunc, maxConc, ch, nil)
	}()

	out := make([]HostExecResult, 0, len(jobs))
	for res := range ch {
		out = append(out, res)
	}
	return out, nil
}

func runOneRemoteSSH(user string, r hosts.Record, remoteCmd string, cache *ClientCache) HostExecResult {
	res := HostExecResult{
		Name:     r.Name,
		IP:       r.PrimaryIP,
		Provider: r.Provider,
	}
	for attempt := 1; attempt <= sshTransientOpAttempts; attempt++ {
		client, dialErr := cache.GetOrDial(user, r)
		if dialErr != nil {
			if attempt < sshTransientOpAttempts && IsSSHConnTransientError(dialErr) {
				evictCachedSSHClient(cache, user, r, attempt)
				continue
			}
			res.Success = false
			res.ErrMsg = dialErr.Error()
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
				if res.ExitCode != 0 {
					res.ErrMsg = fmt.Sprintf("exit %d", res.ExitCode)
				}
				return res
			}
			if attempt < sshTransientOpAttempts && IsSSHConnTransientError(runErr) {
				closeSSHIfEphemeral(cache, client)
				evictCachedSSHClient(cache, user, r, attempt)
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
type SFTPDownloadJob struct {
	Record     hosts.Record
	LocalAbs   string
	RemotePath string
}

// StreamSFTPUploadParallel uploads the same local file to remotePath on each
// record (SFTP over DialHoneyClient). Failures on one host do not cancel others.
func StreamSFTPUploadParallel(user string, recs []hosts.Record, localAbs, remotePath string, maxConc int, out chan<- HostExecResult, cache *ClientCache) error {
	localAbs = strings.TrimSpace(localAbs)
	remotePath = strings.TrimSpace(remotePath)
	if localAbs == "" || remotePath == "" {
		return fmt.Errorf("upload: empty local or remote path")
	}
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
			out <- runOneSFTPUpload(user, r, localAbs, remotePath, cache)
		}(jobs[i])
	}
	wg.Wait()
	return nil
}

// StreamSFTPDownloadParallel downloads files from multiple hosts in parallel.
func StreamSFTPDownloadParallel(user string, jobs []SFTPDownloadJob, maxConc int, out chan<- HostExecResult, cache *ClientCache) error {
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
			out <- runOneSFTPDownload(user, j, cache)
		}(jobs[i])
	}
	wg.Wait()
	return nil
}

// StreamScriptUploadRunParallel uploads a script and executes it on multiple hosts in parallel.
func StreamScriptUploadRunParallel(user string, recs []hosts.Record, localAbs, remotePath string, remoteCmdFunc func(hosts.Record) string, maxConc int, out chan<- HostExecResult, cache *ClientCache) error {
	localAbs = strings.TrimSpace(localAbs)
	remotePath = strings.TrimSpace(remotePath)
	if localAbs == "" || remotePath == "" {
		return fmt.Errorf("script step: empty local, remote path, or remote command")
	}
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
			cmd := remoteCmdFunc(r)
			if strings.TrimSpace(cmd) == "" {
				out <- HostExecResult{Name: r.Name, Provider: r.Provider, Success: true, Output: ""}
				return
			}
			out <- runOneScriptUploadRun(user, r, localAbs, remotePath, cmd, cache)
		}(jobs[i])
	}
	wg.Wait()
	return nil
}

// ExecuteSFTPUploadParallel executes an SFTP upload in parallel across multiple hosts and returns results synchronously.
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
		_ = StreamSFTPUploadParallel(user, recs, localAbs, remotePath, maxConc, ch, nil)
	}()

	out := make([]HostExecResult, 0, len(jobs))
	for res := range ch {
		out = append(out, res)
	}
	return out, nil
}

// ExecuteSFTPDownloadParallel runs each download job (possibly different local
// paths per host) in parallel.
func ExecuteSFTPDownloadParallel(user string, jobs []SFTPDownloadJob, maxConc int) ([]HostExecResult, error) {
	if len(jobs) == 0 {
		return []HostExecResult{}, nil
	}

	ch := make(chan HostExecResult, len(jobs))
	go func() {
		defer close(ch)
		_ = StreamSFTPDownloadParallel(user, jobs, maxConc, ch, nil)
	}()

	out := make([]HostExecResult, 0, len(jobs))
	for res := range ch {
		out = append(out, res)
	}
	return out, nil
}

func runOneSFTPUpload(user string, r hosts.Record, localAbs, remotePath string, cache *ClientCache) HostExecResult {
	res := HostExecResult{Name: r.Name, IP: r.PrimaryIP, Provider: r.Provider}
	for attempt := 1; attempt <= sshTransientOpAttempts; attempt++ {
		client, dialErr := cache.GetOrDial(user, r)
		if dialErr != nil {
			if attempt < sshTransientOpAttempts && IsSSHConnTransientError(dialErr) {
				evictCachedSSHClient(cache, user, r, attempt)
				continue
			}
			res.Success = false
			res.ErrMsg = dialErr.Error()
			return res
		}
		upErr := client.Upload(localAbs, remotePath)
		if upErr != nil {
			if attempt < sshTransientOpAttempts && IsSSHConnTransientError(upErr) {
				closeSSHIfEphemeral(cache, client)
				evictCachedSSHClient(cache, user, r, attempt)
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

// ExecuteScriptUploadRunParallel uploads localAbs to remotePath on each host over SFTP,
// then runs remoteCmd on the same SSH connection (one session per host per step).
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
		_ = StreamScriptUploadRunParallel(user, recs, localAbs, remotePath, func(_ hosts.Record) string { return remoteCmd }, maxConc, ch, nil)
	}()

	out := make([]HostExecResult, 0, len(jobs))
	for res := range ch {
		out = append(out, res)
	}
	return out, nil
}

func runOneScriptUploadRun(user string, r hosts.Record, localAbs, remotePath, remoteCmd string, cache *ClientCache) HostExecResult {
	res := HostExecResult{Name: r.Name, IP: r.PrimaryIP, Provider: r.Provider}
	for attempt := 1; attempt <= sshTransientOpAttempts; attempt++ {
		client, dialErr := cache.GetOrDial(user, r)
		if dialErr != nil {
			if attempt < sshTransientOpAttempts && IsSSHConnTransientError(dialErr) {
				evictCachedSSHClient(cache, user, r, attempt)
				continue
			}
			res.Success = false
			res.ErrMsg = dialErr.Error()
			return res
		}

		if upErr := client.Upload(localAbs, remotePath); upErr != nil {
			if attempt < sshTransientOpAttempts && IsSSHConnTransientError(upErr) {
				closeSSHIfEphemeral(cache, client)
				evictCachedSSHClient(cache, user, r, attempt)
				continue
			}
			closeSSHIfEphemeral(cache, client)
			res.Success = false
			res.ErrMsg = "upload: " + upErr.Error()
			return res
		}

		raw, runErr := client.Run(remoteCmd)
		out := strings.TrimSpace(string(raw))
		if len(out) > maxOutputPerHost {
			out = out[:maxOutputPerHost] + "\n…(truncated)"
		}
		res.Output = "script put → " + remotePath + "\n" + out

		if runErr != nil {
			var ee *ssh.ExitError
			if errors.As(runErr, &ee) {
				closeSSHIfEphemeral(cache, client)
				res.ExitCode = ee.ExitStatus()
				res.Success = false
				if res.ExitCode != 0 {
					res.ErrMsg = fmt.Sprintf("run: exit %d", res.ExitCode)
				}
				return res
			}
			if attempt < sshTransientOpAttempts && IsSSHConnTransientError(runErr) {
				closeSSHIfEphemeral(cache, client)
				evictCachedSSHClient(cache, user, r, attempt)
				continue
			}
			closeSSHIfEphemeral(cache, client)
			res.Success = false
			res.ErrMsg = "run: " + runErr.Error()
			return res
		}
		closeSSHIfEphemeral(cache, client)
		res.Success = true
		res.ExitCode = 0
		return res
	}
	res.Success = false
	res.ErrMsg = "script step: exceeded transient retry attempts"
	return res
}

func runOneSFTPDownload(user string, j SFTPDownloadJob, cache *ClientCache) HostExecResult {
	r := j.Record
	res := HostExecResult{Name: r.Name, IP: r.PrimaryIP, Provider: r.Provider}
	for attempt := 1; attempt <= sshTransientOpAttempts; attempt++ {
		client, dialErr := cache.GetOrDial(user, r)
		if dialErr != nil {
			if attempt < sshTransientOpAttempts && IsSSHConnTransientError(dialErr) {
				evictCachedSSHClient(cache, user, r, attempt)
				continue
			}
			res.Success = false
			res.ErrMsg = dialErr.Error()
			return res
		}
		dlErr := client.Download(j.RemotePath, j.LocalAbs)
		if dlErr != nil {
			if attempt < sshTransientOpAttempts && IsSSHConnTransientError(dlErr) {
				closeSSHIfEphemeral(cache, client)
				evictCachedSSHClient(cache, user, r, attempt)
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
		res.Output = "get " + j.RemotePath + " → " + j.LocalAbs
		return res
	}
	res.Success = false
	res.ErrMsg = "sftp get: exceeded transient retry attempts"
	return res
}

// SortHostExecForUI orders failures first, then host name (case-insensitive).
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
