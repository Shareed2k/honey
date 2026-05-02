package ui

import (
	"errors"
	"fmt"
	"honey/internal/hosts"
	"sort"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

const (
	defaultSSHBatchConcurrency = 32
	maxOutputPerHost           = 6000
)

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
func StreamSSHParallel(user string, jobs []hosts.Record, remoteCmd string, maxConc int, out chan<- HostExecResult, cache *ClientCache) error {
	remoteCmd = strings.TrimSpace(remoteCmd)
	if remoteCmd == "" {
		return nil
	}
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
			out <- runOneRemoteSSH(user, r, remoteCmd, cache)
		}(jobs[i])
	}
	wg.Wait()
	return nil
}

// ExecuteSSHParallel runs the same remote shell command on every record that has
// PrimaryIP set. Failures on individual hosts do not cancel others.
// It uses DialHoneyClient (golang.org/x/crypto/ssh + ~/.ssh/config) with known_hosts verification.
func ExecuteSSHParallel(user string, recs []hosts.Record, remoteCmd string, maxConc int) ([]HostExecResult, error) {
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
		_ = StreamSSHParallel(user, jobs, remoteCmd, maxConc, ch, nil)
	}()

	var out []HostExecResult
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
	client, err := cache.GetOrDial(user, r)
	if err != nil {
		res.Success = false
		res.ErrMsg = err.Error()
		return res
	}
	if cache == nil {
		defer func() { _ = client.Close() }()
	}

	raw, err := client.Run(remoteCmd)
	out := strings.TrimSpace(string(raw))
	if len(out) > maxOutputPerHost {
		out = out[:maxOutputPerHost] + "\n…(truncated)"
	}
	res.Output = out

	if err != nil {
		var ee *ssh.ExitError
		if errors.As(err, &ee) {
			res.ExitCode = ee.ExitStatus()
			res.Success = false
			if res.ExitCode != 0 {
				res.ErrMsg = fmt.Sprintf("exit %d", res.ExitCode)
			}
			return res
		}
		res.Success = false
		res.ErrMsg = err.Error()
		return res
	}
	res.Success = true
	res.ExitCode = 0
	return res
}

// SFTPDownloadJob is one remote→local file copy for a specific host.
type SFTPDownloadJob struct {
	Record     hosts.Record
	LocalAbs   string
	RemotePath string
}

// ExecuteSFTPUploadParallel uploads the same local file to remotePath on each
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
			if strings.TrimSpace(j.Record.PrimaryIP) == "" && !(j.Record.Provider == "k8s" && j.Record.Meta["kind"] == "pod") {
				out <- HostExecResult{Name: j.Record.Name, Provider: j.Record.Provider, Success: false, ErrMsg: "missing PrimaryIP"}
				return
			}
			out <- runOneSFTPDownload(user, j, cache)
		}(jobs[i])
	}
	wg.Wait()
	return nil
}

func StreamScriptUploadRunParallel(user string, recs []hosts.Record, localAbs, remotePath, remoteCmd string, maxConc int, out chan<- HostExecResult, cache *ClientCache) error {
	localAbs = strings.TrimSpace(localAbs)
	remotePath = strings.TrimSpace(remotePath)
	remoteCmd = strings.TrimSpace(remoteCmd)
	if localAbs == "" || remotePath == "" || remoteCmd == "" {
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
			out <- runOneScriptUploadRun(user, r, localAbs, remotePath, remoteCmd, cache)
		}(jobs[i])
	}
	wg.Wait()
	return nil
}
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

	var out []HostExecResult
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

	var out []HostExecResult
	for res := range ch {
		out = append(out, res)
	}
	return out, nil
}

func runOneSFTPUpload(user string, r hosts.Record, localAbs, remotePath string, cache *ClientCache) HostExecResult {
	res := HostExecResult{Name: r.Name, IP: r.PrimaryIP, Provider: r.Provider}
	client, err := cache.GetOrDial(user, r)
	if err != nil {
		res.Success = false
		res.ErrMsg = err.Error()
		return res
	}
	if cache == nil {
		defer func() { _ = client.Close() }()
	}
	if err := client.Upload(localAbs, remotePath); err != nil {
		res.Success = false
		res.ErrMsg = err.Error()
		return res
	}
	res.Success = true
	res.ExitCode = 0
	res.Output = "put " + localAbs + " → " + remotePath
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
		_ = StreamScriptUploadRunParallel(user, recs, localAbs, remotePath, remoteCmd, maxConc, ch, nil)
	}()

	var out []HostExecResult
	for res := range ch {
		out = append(out, res)
	}
	return out, nil
}

func runOneScriptUploadRun(user string, r hosts.Record, localAbs, remotePath, remoteCmd string, cache *ClientCache) HostExecResult {
	res := HostExecResult{Name: r.Name, IP: r.PrimaryIP, Provider: r.Provider}
	client, err := cache.GetOrDial(user, r)
	if err != nil {
		res.Success = false
		res.ErrMsg = err.Error()
		return res
	}
	if cache == nil {
		defer func() { _ = client.Close() }()
	}

	if err := client.Upload(localAbs, remotePath); err != nil {
		res.Success = false
		res.ErrMsg = "upload: " + err.Error()
		return res
	}

	raw, err := client.Run(remoteCmd)
	out := strings.TrimSpace(string(raw))
	if len(out) > maxOutputPerHost {
		out = out[:maxOutputPerHost] + "\n…(truncated)"
	}
	res.Output = "script put → " + remotePath + "\n" + out

	if err != nil {
		var ee *ssh.ExitError
		if errors.As(err, &ee) {
			res.ExitCode = ee.ExitStatus()
			res.Success = false
			if res.ExitCode != 0 {
				res.ErrMsg = fmt.Sprintf("run: exit %d", res.ExitCode)
			}
			return res
		}
		res.Success = false
		res.ErrMsg = "run: " + err.Error()
		return res
	}
	res.Success = true
	res.ExitCode = 0
	return res
}

func runOneSFTPDownload(user string, j SFTPDownloadJob, cache *ClientCache) HostExecResult {
	r := j.Record
	res := HostExecResult{Name: r.Name, IP: r.PrimaryIP, Provider: r.Provider}
	client, err := cache.GetOrDial(user, r)
	if err != nil {
		res.Success = false
		res.ErrMsg = err.Error()
		return res
	}
	if cache == nil {
		defer func() { _ = client.Close() }()
	}
	if err := client.Download(j.RemotePath, j.LocalAbs); err != nil {
		res.Success = false
		res.ErrMsg = err.Error()
		return res
	}
	res.Success = true
	res.ExitCode = 0
	res.Output = "get " + j.RemotePath + " → " + j.LocalAbs
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
