package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/melbahja/goph"
	"golang.org/x/crypto/ssh"

	"hostctl/internal/hosts"
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

// buildGophAuth mirrors common OpenSSH behavior: try the agent (if present), then
// default private keys under ~/.ssh.
func buildGophAuth() (goph.Auth, error) {
	var methods []ssh.AuthMethod
	if goph.HasAgent() {
		if ag, err := goph.UseAgent(); err == nil {
			methods = append(methods, ag...)
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("user home: %w", err)
	}
	for _, name := range []string{"id_ed25519", "id_rsa", "id_ecdsa"} {
		p := filepath.Join(home, ".ssh", name)
		st, statErr := os.Stat(p)
		if statErr != nil || st.IsDir() {
			continue
		}
		k, keyErr := goph.Key(p, "")
		if keyErr != nil {
			continue
		}
		methods = append(methods, k...)
	}
	if len(methods) == 0 {
		return nil, fmt.Errorf("no SSH auth (start ssh-agent or add ~/.ssh/id_ed25519, id_rsa, or id_ecdsa)")
	}
	return methods, nil
}

// ExecuteSSHParallel runs the same remote shell command on every record that has
// PrimaryIP set. Failures on individual hosts do not cancel others.
// It uses the goph client (golang.org/x/crypto/ssh) with known_hosts verification.
func ExecuteSSHParallel(user string, recs []hosts.Record, remoteCmd string, maxConc int) ([]HostExecResult, error) {
	remoteCmd = strings.TrimSpace(remoteCmd)
	if remoteCmd == "" {
		return []HostExecResult{}, nil
	}
	if maxConc <= 0 {
		maxConc = defaultSSHBatchConcurrency
	}

	var jobs []hosts.Record
	for _, r := range recs {
		if strings.TrimSpace(r.PrimaryIP) != "" {
			jobs = append(jobs, r)
		}
	}
	if len(jobs) == 0 {
		return []HostExecResult{}, nil
	}

	auth, err := buildGophAuth()
	if err != nil {
		return nil, err
	}

	sem := make(chan struct{}, maxConc)
	var wg sync.WaitGroup
	out := make([]HostExecResult, len(jobs))
	for i := range jobs {
		wg.Add(1)
		go func(i int, r hosts.Record) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = runOneRemoteSSH(user, r, remoteCmd, auth)
		}(i, jobs[i])
	}
	wg.Wait()
	return out, nil
}

func runOneRemoteSSH(user string, r hosts.Record, remoteCmd string, auth goph.Auth) HostExecResult {
	res := HostExecResult{
		Name:     r.Name,
		IP:       r.PrimaryIP,
		Provider: r.Provider,
	}
	client, err := goph.New(user, r.PrimaryIP, auth)
	if err != nil {
		res.Success = false
		res.ErrMsg = err.Error()
		return res
	}
	defer func() { _ = client.Close() }()

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
// record (goph SFTP). Failures on one host do not cancel others.
func ExecuteSFTPUploadParallel(user string, recs []hosts.Record, localAbs, remotePath string, maxConc int) ([]HostExecResult, error) {
	localAbs = strings.TrimSpace(localAbs)
	remotePath = strings.TrimSpace(remotePath)
	if localAbs == "" || remotePath == "" {
		return nil, fmt.Errorf("upload: empty local or remote path")
	}
	if maxConc <= 0 {
		maxConc = defaultSSHBatchConcurrency
	}
	var jobs []hosts.Record
	for _, r := range recs {
		if strings.TrimSpace(r.PrimaryIP) != "" {
			jobs = append(jobs, r)
		}
	}
	if len(jobs) == 0 {
		return []HostExecResult{}, nil
	}
	auth, err := buildGophAuth()
	if err != nil {
		return nil, err
	}
	sem := make(chan struct{}, maxConc)
	var wg sync.WaitGroup
	out := make([]HostExecResult, len(jobs))
	for i := range jobs {
		wg.Add(1)
		go func(i int, r hosts.Record) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = runOneSFTPUpload(user, r, localAbs, remotePath, auth)
		}(i, jobs[i])
	}
	wg.Wait()
	return out, nil
}

// ExecuteSFTPDownloadParallel runs each download job (possibly different local
// paths per host) in parallel.
func ExecuteSFTPDownloadParallel(user string, jobs []SFTPDownloadJob, maxConc int) ([]HostExecResult, error) {
	if maxConc <= 0 {
		maxConc = defaultSSHBatchConcurrency
	}
	if len(jobs) == 0 {
		return []HostExecResult{}, nil
	}
	auth, err := buildGophAuth()
	if err != nil {
		return nil, err
	}
	sem := make(chan struct{}, maxConc)
	var wg sync.WaitGroup
	out := make([]HostExecResult, len(jobs))
	for i := range jobs {
		wg.Add(1)
		go func(i int, j SFTPDownloadJob) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if strings.TrimSpace(j.Record.PrimaryIP) == "" {
				out[i] = HostExecResult{Name: j.Record.Name, Provider: j.Record.Provider, Success: false, ErrMsg: "missing PrimaryIP"}
				return
			}
			out[i] = runOneSFTPDownload(user, j, auth)
		}(i, jobs[i])
	}
	wg.Wait()
	return out, nil
}

func runOneSFTPUpload(user string, r hosts.Record, localAbs, remotePath string, auth goph.Auth) HostExecResult {
	res := HostExecResult{Name: r.Name, IP: r.PrimaryIP, Provider: r.Provider}
	client, err := goph.New(user, r.PrimaryIP, auth)
	if err != nil {
		res.Success = false
		res.ErrMsg = err.Error()
		return res
	}
	defer func() { _ = client.Close() }()
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
	localAbs = strings.TrimSpace(localAbs)
	remotePath = strings.TrimSpace(remotePath)
	remoteCmd = strings.TrimSpace(remoteCmd)
	if localAbs == "" || remotePath == "" || remoteCmd == "" {
		return nil, fmt.Errorf("script step: empty local, remote path, or remote command")
	}
	if maxConc <= 0 {
		maxConc = defaultSSHBatchConcurrency
	}
	var jobs []hosts.Record
	for _, r := range recs {
		if strings.TrimSpace(r.PrimaryIP) != "" {
			jobs = append(jobs, r)
		}
	}
	if len(jobs) == 0 {
		return []HostExecResult{}, nil
	}
	auth, err := buildGophAuth()
	if err != nil {
		return nil, err
	}
	sem := make(chan struct{}, maxConc)
	var wg sync.WaitGroup
	out := make([]HostExecResult, len(jobs))
	for i := range jobs {
		wg.Add(1)
		go func(i int, r hosts.Record) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = runOneScriptUploadRun(user, r, localAbs, remotePath, remoteCmd, auth)
		}(i, jobs[i])
	}
	wg.Wait()
	return out, nil
}

func runOneScriptUploadRun(user string, r hosts.Record, localAbs, remotePath, remoteCmd string, auth goph.Auth) HostExecResult {
	res := HostExecResult{Name: r.Name, IP: r.PrimaryIP, Provider: r.Provider}
	client, err := goph.New(user, r.PrimaryIP, auth)
	if err != nil {
		res.Success = false
		res.ErrMsg = err.Error()
		return res
	}
	defer func() { _ = client.Close() }()

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

func runOneSFTPDownload(user string, j SFTPDownloadJob, auth goph.Auth) HostExecResult {
	r := j.Record
	res := HostExecResult{Name: r.Name, IP: r.PrimaryIP, Provider: r.Provider}
	client, err := goph.New(user, r.PrimaryIP, auth)
	if err != nil {
		res.Success = false
		res.ErrMsg = err.Error()
		return res
	}
	defer func() { _ = client.Close() }()
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
