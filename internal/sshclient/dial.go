// Package sshclient provides SSH client dialing, SFTP, tunnels, and known_hosts helpers for honey.
package sshclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kevinburke/ssh_config"
	"github.com/melbahja/goph"
	"github.com/pkg/sftp"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/safepath"
)

// honeySSHConfig uses kevinburke/ssh_config (https://github.com/kevinburke/ssh_config) to read
// ~/.ssh/config and /etc/ssh/ssh_config. Parse errors are ignored so a broken file does not block
// direct connects (see IgnoreErrors on UserSettings).
//
// Supported directives for the lookup alias (e.g. instance IP): User, HostName, Port,
// IdentityFile, ProxyJump (comma-separated hops), StrictHostKeyChecking (yes/ask/accept-new/no).
// Honey defaults to accept-new for unknown keys unless HONEY_SSH_STRICT_HOSTKEYS=1. Only "yes" is strict;
// "ask" is treated as accept-new (the ssh_config parser also defaults to "ask" when unset).
//
// Note: "Match" is unsupported by that parser; configs that rely on Match may not apply here.
var honeySSHConfig = &ssh_config.UserSettings{IgnoreErrors: true}

// honeySSHIdentityFilesEnv lists extra private key paths (comma-separated) tried after IdentityFile
// entries from ssh_config and before default ~/.ssh key names. Values support ~/ prefix.
const honeySSHIdentityFilesEnv = "HONEY_SSH_IDENTITY_FILES"

// knownHostsAppendMu serializes writes to ~/.ssh/known_hosts when many parallel SSH sessions accept-new.
var knownHostsAppendMu sync.Mutex

// hostKeyCallbackForAlias returns a HostKeyCallback for the given ssh_config alias (e.g. instance IP).
// It loads OpenSSH-style known_hosts files (see https://pkg.go.dev/golang.org/x/crypto/ssh/knownhosts):
// paths from HONEY_SSH_KNOWN_HOSTS (comma-separated, optional), UserKnownHostsFile / GlobalKnownHostsFile
// from ssh_config (if set), then ~/.ssh/known_hosts, ~/.ssh/google_compute_engine_known_hosts (common for gcloud),
// ~/.ssh/known_hosts2, and system ssh_known_hosts.
//
// StrictHostKeyChecking from ~/.ssh/config: "no" disables verification; "yes" requires a known key.
// "ask", "accept-new", and the ssh_config default ("ask" when no directive matches) are treated like
// accept-new here: honey is non-interactive, so unknown keys are written once to ~/.ssh/known_hosts.
// Set HONEY_SSH_STRICT_HOSTKEYS=1 to require known keys (and fail if the host is not listed).
//
// Stale host keys are renewed by default (matching known_hosts lines removed in pure Go, then append the new key).
// Set HONEY_SSH_RENEW_STALE_HOST_KEYS=0 (or false/no/off) to disable stale-key renewal. Useful after VM rebuilds; weaker against MITM during renewal.
func hostKeyCallbackForAlias(alias string) (ssh.HostKeyCallback, error) {
	strictSSH := strings.ToLower(strings.TrimSpace(honeySSHConfig.Get(alias, "StrictHostKeyChecking")))
	if strictSSH == "no" {
		// #nosec G106 -- mirrors OpenSSH StrictHostKeyChecking=no (explicit ssh_config opt-out of host key checking).
		return ssh.InsecureIgnoreHostKey(), nil
	}

	envStrict := strings.ToLower(strings.TrimSpace(os.Getenv("HONEY_SSH_STRICT_HOSTKEYS")))
	// Only "yes" is strict — not "ask": kevinburke/ssh_config defaults StrictHostKeyChecking to "ask"
	// when unset, which would otherwise block accept-new for every host.
	useStrict := strictSSH == "yes" || envStrict == "1" || envStrict == "yes" || envStrict == "true"

	paths := collectKnownHostsPaths(alias)
	if len(paths) == 0 {
		if useStrict {
			return nil, fmt.Errorf("no readable known_hosts files found; create ~/.ssh/known_hosts (optional: HONEY_SSH_KNOWN_HOSTS=comma-separated paths). Unset HONEY_SSH_STRICT_HOSTKEYS to auto-create ~/.ssh/known_hosts on first connect")
		}
		p, err := ensureUserKnownHostsFile()
		if err != nil {
			return nil, err
		}
		paths = []string{p}
	}
	inner, err := knownhosts.New(paths...)
	if err != nil {
		return nil, err
	}
	writeTo, err := userKnownHostsWritePath()
	if err != nil {
		return nil, err
	}
	return buildHostKeyCallback(inner, paths, len(paths), !useStrict, writeTo, honeySSHAutoRenewStaleHostKeys()), nil
}

func ensureUserKnownHostsFile() (p string, err error) {
	p, err = userKnownHostsWritePath()
	if err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	homeAbs, err := filepath.Abs(filepath.Clean(home))
	if err != nil {
		return "", err
	}
	dirAbs, err := filepath.Abs(filepath.Clean(filepath.Dir(p)))
	if err != nil {
		return "", err
	}
	if err := safepath.Under(homeAbs, dirAbs); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dirAbs, 0o700); err != nil {
		return "", err
	}
	r, err := os.OpenRoot(dirAbs)
	if err != nil {
		return "", err
	}
	defer func() {
		if cerr := r.Close(); cerr != nil && err == nil {
			err = cerr
			p = ""
		}
	}()
	f, err := r.OpenFile(filepath.Base(p), os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return "", err
	}
	return p, f.Close()
}

func userKnownHostsWritePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "known_hosts"), nil
}

// buildHostKeyCallback wraps knownhosts: on unknown host, optionally appends the key (accept-new); on mismatch, fails or renews.
func buildHostKeyCallback(inner ssh.HostKeyCallback, knownHostsPaths []string, knownFiles int, acceptNew bool, writeTo string, renewStale bool) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := inner(hostname, remote, key)
		if err == nil {
			return nil
		}
		var ke *knownhosts.KeyError
		if !errors.As(err, &ke) {
			return err
		}
		if len(ke.Want) > 0 {
			if !renewStale {
				return fmt.Errorf("%w: host key differs from known_hosts (possible MITM or server rebuild); stale renewal is off (set HONEY_SSH_RENEW_STALE_HOST_KEYS=0). Re-enable default renewal by unsetting that var, or fix with: ssh-keygen -R <host>", ke)
			}
			knownHostsAppendMu.Lock()
			defer knownHostsAppendMu.Unlock()
			if rerr := removeHostFromKnownHostsFiles(knownHostsPaths, hostname, remote); rerr != nil {
				return fmt.Errorf("%w: key mismatch and auto-renew failed: %v", ke, rerr)
			}
			_ = os.MkdirAll(filepath.Dir(writeTo), 0o700)
			if werr := goph.AddKnownHost(hostname, remote, key, writeTo); werr != nil {
				return fmt.Errorf("%w: removed stale keys but could not append new key to %s: %v", ke, writeTo, werr)
			}
			return nil
		}
		if acceptNew {
			_ = os.MkdirAll(filepath.Dir(writeTo), 0o700)
			knownHostsAppendMu.Lock()
			werr := goph.AddKnownHost(hostname, remote, key, writeTo)
			knownHostsAppendMu.Unlock()
			if werr != nil {
				return fmt.Errorf("%w: unknown host and could not append key to %s: %v", ke, writeTo, werr)
			}
			return nil
		}
		tip := strings.TrimSpace(hostname)
		if tip == "" && remote != nil {
			tip = remote.String()
		}
		return fmt.Errorf("%w: host not listed in known_hosts (checked %d file(s)); add with: %s >> ~/.ssh/known_hosts (or unset HONEY_SSH_STRICT_HOSTKEYS to allow auto-trust once)", ke, knownFiles, sshKeyscanCLIPrompt(tip))
	}
}

// sshKeyscanCLIPrompt returns a copy-paste ssh-keyscan fragment (no shell redirection).
// ssh-keyscan expects host and optional -p PORT; it does not accept host:port as a single argument.
func sshKeyscanCLIPrompt(hostOrHostPort string) string {
	s := strings.TrimSpace(hostOrHostPort)
	if s == "" || s == "HOST:22" {
		return "ssh-keyscan -H HOST"
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return fmt.Sprintf("ssh-keyscan -H %s", s)
	}
	if port == "" || port == "22" {
		return fmt.Sprintf("ssh-keyscan -H %s", host)
	}
	return fmt.Sprintf("ssh-keyscan -p %s -H %s", port, host)
}

func collectKnownHostsPaths(alias string) []string {
	seen := make(map[string]struct{})
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(strings.Trim(p, `"`))
		if p == "" || p == "/dev/null" {
			return
		}
		if strings.HasPrefix(p, "~/") {
			home, herr := os.UserHomeDir()
			if herr != nil {
				return
			}
			p = filepath.Join(home, strings.TrimPrefix(p, "~/"))
		}
		p = filepath.Clean(p)
		if _, dup := seen[p]; dup {
			return
		}
		st, statErr := os.Stat(p)
		if statErr != nil || st.IsDir() {
			return
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}

	if extra := strings.TrimSpace(os.Getenv("HONEY_SSH_KNOWN_HOSTS")); extra != "" {
		for _, part := range strings.Split(extra, ",") {
			add(strings.TrimSpace(part))
		}
	}

	for _, key := range []string{"UserKnownHostsFile", "GlobalKnownHostsFile"} {
		for _, v := range honeySSHConfig.GetAll(alias, key) {
			for _, part := range strings.Fields(strings.TrimSpace(v)) {
				add(part)
			}
		}
	}

	home, herr := os.UserHomeDir()
	if herr == nil {
		for _, p := range []string{
			filepath.Join(home, ".ssh", "known_hosts"),
			filepath.Join(home, ".ssh", "google_compute_engine_known_hosts"),
			filepath.Join(home, ".ssh", "known_hosts2"),
		} {
			add(p)
		}
	}
	if runtime.GOOS != "windows" {
		for _, p := range []string{"/etc/ssh/ssh_known_hosts", "/etc/ssh/ssh_known_hosts2"} {
			add(p)
		}
	}

	return out
}

// HoneyClient wraps goph.Client so Close() also shuts down ProxyJump bastion clients.
type HoneyClient struct {
	*goph.Client
	parents []*ssh.Client

	sftpMu sync.Mutex
	sftp   *sftp.Client
}

// LeafSSH returns the leaf *ssh.Client used for sessions/SFTP on the target host (ProxyJump hops are parents).
func (h *HoneyClient) LeafSSH() *ssh.Client {
	if h == nil || h.Client == nil || h.Client.Client == nil {
		return nil
	}
	return h.Client.Client
}

// Close closes the target session transport, then any bastion SSH clients (reverse order).
func (h *HoneyClient) Close() error {
	var err error
	h.sftpMu.Lock()
	if h.sftp != nil {
		_ = h.sftp.Close()
		h.sftp = nil
	}
	h.sftpMu.Unlock()
	if h.Client != nil && h.Client.Client != nil {
		err = h.Client.Close()
	}
	for i := len(h.parents) - 1; i >= 0; i-- {
		if h.parents[i] != nil {
			_ = h.parents[i].Close()
		}
	}
	return err
}

func (h *HoneyClient) sftpClient() (*sftp.Client, error) {
	h.sftpMu.Lock()
	defer h.sftpMu.Unlock()
	if h.sftp != nil {
		return h.sftp, nil
	}
	if h.Client == nil || h.Client.Client == nil {
		return nil, fmt.Errorf("ssh client is not connected")
	}
	c, err := sftp.NewClient(h.Client.Client)
	if err != nil {
		return nil, err
	}
	h.sftp = c
	return c, nil
}

// sftpUploadProgressReader wraps the local file reader and emits throttled progress callbacks.
type sftpUploadProgressReader struct {
	r       io.Reader
	total   int64
	step    int64
	minGap  time.Duration
	written int64
	last    int64
	lastAt  time.Time
	on      func(written, total int64)
}

func (p *sftpUploadProgressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.written += int64(n)
		if p.on == nil {
			return n, err
		}
		now := time.Now()
		overBytes := p.written-p.last >= p.step
		overTime := p.minGap > 0 && now.Sub(p.lastAt) >= p.minGap
		atEnd := p.total > 0 && p.written >= p.total
		if overBytes || overTime || atEnd {
			p.on(p.written, p.total)
			p.last = p.written
			p.lastAt = now
		}
	}
	return n, err
}

// Upload copies a local file to the remote path over SFTP.
func (h *HoneyClient) Upload(localPath, remotePath string) error {
	return h.UploadWithProgress(localPath, remotePath, nil)
}

// UploadWithProgress copies a local file to the remote path over SFTP, calling onProgress
// with cumulative bytes written to the remote and the local file size (throttled).
// onProgress may be nil.
func (h *HoneyClient) UploadWithProgress(localPath, remotePath string, onProgress func(written, total int64)) error {
	localPath = strings.TrimSpace(localPath)
	remotePath = strings.TrimSpace(remotePath)
	if localPath == "" || remotePath == "" {
		return fmt.Errorf("upload: empty local or remote path")
	}
	// Trailing slash means "directory": use the local file's base name on the server.
	if strings.HasSuffix(remotePath, "/") {
		base := filepath.Base(localPath)
		if base == "." || base == ".." || base == "/" || base == "" {
			return fmt.Errorf("upload: need a file name inside %q (local path has no usable base name)", remotePath)
		}
		remotePath = path.Join(strings.TrimRight(remotePath, "/"), base)
	}
	sftpClient, err := h.sftpClient()
	if err != nil {
		return err
	}
	in, err := os.Open(localPath) // #nosec G304 -- caller controls local path in CLI/web flows.
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	total := st.Size()
	if onProgress != nil {
		onProgress(0, total)
	}
	if err := sftpClient.MkdirAll(filepath.ToSlash(filepath.Dir(remotePath))); err != nil {
		return err
	}
	// Open WRONLY+CREATE+TRUNC instead of Create (RDWR): some SFTP servers
	// (e.g. AWS Transfer Family) reject read/write opens and return SSH_FX_FAILURE.
	out, err := sftpClient.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	var body io.Reader = in
	if onProgress != nil {
		body = &sftpUploadProgressReader{
			r:      in,
			total:  total,
			step:   256 << 10,
			minGap: 200 * time.Millisecond,
			on:     onProgress,
		}
	}
	if _, err := io.Copy(out, body); err != nil {
		return err
	}
	if onProgress != nil {
		onProgress(total, total)
	}
	return nil
}

// Download copies a remote file to a local path over SFTP.
func (h *HoneyClient) Download(remotePath, localPath string) error {
	remotePath = strings.TrimSpace(remotePath)
	localPath = strings.TrimSpace(localPath)
	if localPath == "" || remotePath == "" {
		return fmt.Errorf("download: empty local or remote path")
	}
	sftpClient, err := h.sftpClient()
	if err != nil {
		return err
	}
	in, err := sftpClient.Open(remotePath)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(localPath), 0o750); err != nil {
		return err
	}
	out, err := os.OpenFile(localPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600) // #nosec G304 -- caller controls destination.
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}

// ListRemoteDir returns sorted directory entries for the given remote path.
func (h *HoneyClient) ListRemoteDir(path string) ([]hostexec.RemoteFileEntry, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "."
	}
	sftpClient, err := h.sftpClient()
	if err != nil {
		return nil, err
	}
	entries, err := sftpClient.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]hostexec.RemoteFileEntry, 0, len(entries))
	for _, ent := range entries {
		if ent == nil {
			continue
		}
		out = append(out, hostexec.RemoteFileEntry{
			Name:       ent.Name(),
			Path:       filepath.ToSlash(filepath.Join(path, ent.Name())),
			IsDir:      ent.IsDir(),
			Size:       ent.Size(),
			Mode:       ent.Mode().String(),
			ModifiedAt: ent.ModTime(),
		})
	}
	slices.SortFunc(out, func(a, b hostexec.RemoteFileEntry) int {
		if a.IsDir != b.IsDir {
			if a.IsDir {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out, nil
}

// StatRemote returns metadata for a single remote filesystem object.
func (h *HoneyClient) StatRemote(path string) (hostexec.RemoteFileEntry, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return hostexec.RemoteFileEntry{}, fmt.Errorf("empty path")
	}
	sftpClient, err := h.sftpClient()
	if err != nil {
		return hostexec.RemoteFileEntry{}, err
	}
	ent, err := sftpClient.Stat(path)
	if err != nil {
		return hostexec.RemoteFileEntry{}, err
	}
	return hostexec.RemoteFileEntry{
		Name:       filepath.Base(path),
		Path:       filepath.ToSlash(path),
		IsDir:      ent.IsDir(),
		Size:       ent.Size(),
		Mode:       ent.Mode().String(),
		ModifiedAt: ent.ModTime(),
	}, nil
}

// MkdirAllRemote creates a remote directory tree via SFTP.
func (h *HoneyClient) MkdirAllRemote(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("empty path")
	}
	sftpClient, err := h.sftpClient()
	if err != nil {
		return err
	}
	return sftpClient.MkdirAll(path)
}

// RemoveRemote deletes a remote file or directory (recursive walks children first).
func (h *HoneyClient) RemoveRemote(path string, recursive bool) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("empty path")
	}
	sftpClient, err := h.sftpClient()
	if err != nil {
		return err
	}
	if !recursive {
		return sftpClient.Remove(path)
	}
	w := sftpClient.Walk(path)
	var files []string
	var dirs []string
	for w.Step() {
		if w.Err() != nil {
			return w.Err()
		}
		p := w.Path()
		if p == "." || p == "" {
			continue
		}
		info := w.Stat()
		if info == nil {
			continue
		}
		if info.IsDir() {
			dirs = append(dirs, p)
		} else {
			files = append(files, p)
		}
	}
	for _, filePath := range files {
		if err := sftpClient.Remove(filePath); err != nil {
			return err
		}
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := sftpClient.RemoveDirectory(dirs[i]); err != nil {
			return err
		}
	}
	return nil
}

// RunWithStreams runs cmd on the remote (non-interactive session) with stdin/stdout/stderr attached.
// stderr may be nil to discard remote stderr.
func (h *HoneyClient) RunWithStreams(cmd string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return fmt.Errorf("empty remote command")
	}
	if h.Client == nil || h.Client.Client == nil {
		return fmt.Errorf("ssh client is not connected")
	}
	sess, err := h.NewSession()
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	sess.Stdin = stdin
	sess.Stdout = stdout
	if stderr != nil {
		sess.Stderr = stderr
	} else {
		sess.Stderr = io.Discard
	}
	return sess.Run(cmd)
}

func expandSSHPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, `"`)
	if p == "" || p == "none" {
		return "", nil
	}
	if strings.HasPrefix(p, "~/") {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", herr
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~/"))
	}
	return filepath.Clean(p), nil
}

// splitCommaNonEmpty splits s on commas, trims ASCII space, drops empty tokens.
func splitCommaNonEmpty(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// identityPathsFromHoneyEnv returns paths from HONEY_SSH_IDENTITY_FILES (comma-separated, ~/ expanded).
func identityPathsFromHoneyEnv() ([]string, error) {
	raw := strings.TrimSpace(os.Getenv(honeySSHIdentityFilesEnv))
	if raw == "" {
		return nil, nil
	}
	var out []string
	for _, tok := range splitCommaNonEmpty(raw) {
		p, err := expandSSHPath(tok)
		if err != nil {
			return nil, fmt.Errorf("%s: expand %q: %w", honeySSHIdentityFilesEnv, tok, err)
		}
		if p != "" {
			out = append(out, p)
		}
	}
	return out, nil
}

// defaultSSHIdentityKeyBaseNames are tried under ~/.ssh/ after IdentityFile and HONEY_SSH_IDENTITY_FILES.
// Order matches common OpenSSH defaults, then GCE tooling, then legacy DSA (only used if the file exists).
func defaultSSHIdentityKeyBaseNames() []string {
	return []string{"id_ed25519", "id_rsa", "id_ecdsa", "google_compute_engine", "id_dsa"}
}

func appendAuthFromKeyFiles(methods []ssh.AuthMethod, seen map[string]struct{}, paths []string) ([]ssh.AuthMethod, error) {
	for _, raw := range paths {
		p, err := expandSSHPath(raw)
		if err != nil {
			return methods, err
		}
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		st, statErr := os.Stat(p)
		if statErr != nil || st.IsDir() {
			continue
		}
		k, keyErr := goph.Key(p, "")
		if keyErr != nil {
			continue
		}
		seen[p] = struct{}{}
		methods = append(methods, k...)
	}
	return methods, nil
}

func errNoSSHAuth() error {
	return fmt.Errorf("no SSH auth: ensure ssh-agent has a key (ssh-add; check SSH_AUTH_SOCK), "+
		"or add IdentityFile in ~/.ssh/config for the host/IP honey uses (honey's parser does not support Match blocks), "+
		"or set %s to comma-separated private key paths, "+
		"or place a default key under ~/.ssh (id_ed25519, id_rsa, id_ecdsa, google_compute_engine for GCE)",
		honeySSHIdentityFilesEnv)
}

// buildAuthWithIdentityFiles returns auth methods: agent (if any), then extra key files (ssh_config IdentityFile),
// then HONEY_SSH_IDENTITY_FILES, then default ~/.ssh key names (see defaultSSHIdentityKeyBaseNames).
func buildAuthWithIdentityFiles(extraFiles []string) (goph.Auth, error) {
	var methods []ssh.AuthMethod
	if goph.HasAgent() {
		if ag, err := goph.UseAgent(); err == nil {
			methods = append(methods, ag...)
		}
	}
	seen := make(map[string]struct{})
	var err error
	methods, err = appendAuthFromKeyFiles(methods, seen, extraFiles)
	if err != nil {
		return nil, err
	}
	envPaths, err := identityPathsFromHoneyEnv()
	if err != nil {
		return nil, err
	}
	methods, err = appendAuthFromKeyFiles(methods, seen, envPaths)
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("user home: %w", err)
	}
	for _, name := range defaultSSHIdentityKeyBaseNames() {
		p := filepath.Join(home, ".ssh", name)
		methods, err = appendAuthFromKeyFiles(methods, seen, []string{p})
		if err != nil {
			return nil, err
		}
	}
	if len(methods) == 0 {
		return nil, errNoSSHAuth()
	}
	return methods, nil
}

func defaultLocalUser() string {
	if u := strings.TrimSpace(os.Getenv("USER")); u != "" {
		return u
	}
	if u := strings.TrimSpace(os.Getenv("USERNAME")); u != "" {
		return u
	}
	return "root"
}

func resolveOne(alias, userOverride string) resolvedSSH {
	alias = strings.TrimSpace(alias)
	u0 := strings.TrimSpace(userOverride)

	confUser := strings.TrimSpace(honeySSHConfig.Get(alias, "User"))
	hostName := strings.TrimSpace(honeySSHConfig.Get(alias, "HostName"))
	if hostName == "" {
		hostName = alias
	}
	port := 22
	if ps := strings.TrimSpace(honeySSHConfig.Get(alias, "Port")); ps != "" {
		if p, err := strconv.Atoi(ps); err == nil && p > 0 && p < 65536 {
			port = p
		}
	}
	user := u0
	if user == "" {
		user = confUser
	}
	if user == "" {
		user = defaultLocalUser()
	}
	return resolvedSSH{user: user, host: hostName, port: port}
}

type resolvedSSH struct {
	user string
	host string
	port int
}

func collectIdentityFiles(alias string) ([]string, error) {
	files := honeySSHConfig.GetAll(alias, "IdentityFile")
	if len(files) == 0 {
		return nil, nil
	}
	var out []string
	for _, f := range files {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		ep, err := expandSSHPath(f)
		if err != nil {
			return nil, err
		}
		if ep != "" {
			out = append(out, ep)
		}
	}
	return out, nil
}

func parseProxyJumpChain(proxyJump string) []string {
	proxyJump = strings.TrimSpace(proxyJump)
	if proxyJump == "" {
		return nil
	}
	var hops []string
	for _, p := range strings.Split(proxyJump, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			hops = append(hops, p)
		}
	}
	return hops
}

// parseJumpSpec parses "user@host:port", "user@host", "host:port", or "host".
// portFromSpec is true when the hop string included an explicit host:port (including :22).
func parseJumpSpec(spec string) (userPart, host string, port int, portFromSpec bool, err error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", "", 0, false, fmt.Errorf("empty ProxyJump hop")
	}
	port = 22
	userPart = ""
	hostPort := spec
	if i := strings.IndexByte(spec, '@'); i >= 0 {
		userPart = spec[:i]
		hostPort = spec[i+1:]
	}
	host = hostPort
	if h, p, splitErr := net.SplitHostPort(hostPort); splitErr == nil {
		host = h
		if po, aerr := strconv.Atoi(p); aerr == nil && po > 0 && po < 65536 {
			port = po
			portFromSpec = true
		}
	}
	return userPart, host, port, portFromSpec, nil
}

func clientConfig(user string, auth goph.Auth, hk ssh.HostKeyCallback) *ssh.ClientConfig {
	return &ssh.ClientConfig{
		User:            user,
		Auth:            auth,
		HostKeyCallback: hk,
		Timeout:         goph.DefaultTimeout,
	}
}

func closeSSHStack(stack []*ssh.Client) {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] != nil {
			_ = stack[i].Close()
		}
	}
}

// DialHoneyClient opens SSH using ~/.ssh/config (User, HostName, Port, IdentityFile, ProxyJump,
// StrictHostKeyChecking, UserKnownHostsFile, GlobalKnownHostsFile) and known_hosts verification
// via golang.org/x/crypto/ssh/knownhosts (see hostKeyCallbackForAlias). Auth also uses
// HONEY_SSH_IDENTITY_FILES and default ~/.ssh key names (see buildAuthWithIdentityFiles).
func DialHoneyClient(userOverride, hostAlias string) (*HoneyClient, error) {
	zap.L().Debug("dialing honey client", zap.String("host", hostAlias), zap.String("user", userOverride))
	hostAlias = strings.TrimSpace(hostAlias)
	if hostAlias == "" {
		return nil, fmt.Errorf("empty host")
	}
	hkFinal, err := hostKeyCallbackForAlias(hostAlias)
	if err != nil {
		return nil, err
	}
	final := resolveOne(hostAlias, userOverride)
	idFiles, err := collectIdentityFiles(hostAlias)
	if err != nil {
		return nil, err
	}
	for _, hop := range parseProxyJumpChain(honeySSHConfig.Get(hostAlias, "ProxyJump")) {
		_, hopAlias, _, _, perr := parseJumpSpec(hop)
		if perr != nil || hopAlias == "" {
			continue
		}
		more, ierr := collectIdentityFiles(hopAlias)
		if ierr != nil {
			return nil, ierr
		}
		idFiles = append(idFiles, more...)
	}
	auth, err := buildAuthWithIdentityFiles(idFiles)
	if err != nil {
		return nil, err
	}

	jumps := parseProxyJumpChain(honeySSHConfig.Get(hostAlias, "ProxyJump"))
	stack := make([]*ssh.Client, 0, len(jumps)+1)
	var cur *ssh.Client

	for _, hopSpec := range jumps {
		explicitUser, hopHost, specPort, portFromSpec, perr := parseJumpSpec(hopSpec)
		if perr != nil {
			closeSSHStack(stack)
			return nil, perr
		}
		res := resolveOne(hopHost, explicitUser)
		hkHop, herr := hostKeyCallbackForAlias(hopHost)
		if herr != nil {
			closeSSHStack(stack)
			return nil, herr
		}
		hopPort := res.port
		if portFromSpec {
			hopPort = specPort
		}
		hopAddr := net.JoinHostPort(res.host, strconv.Itoa(hopPort))
		if cur == nil {
			c, derr := ssh.Dial("tcp", hopAddr, clientConfig(res.user, auth, hkHop))
			if derr != nil {
				closeSSHStack(stack)
				return nil, fmt.Errorf("proxyjump hop %q: %w", hopSpec, derr)
			}
			stack = append(stack, c)
			cur = c
			continue
		}
		rawConn, derr := cur.Dial("tcp", hopAddr)
		if derr != nil {
			closeSSHStack(stack)
			return nil, fmt.Errorf("proxyjump dial %q: %w", hopSpec, derr)
		}
		ncc, chans, reqs, nerr := ssh.NewClientConn(rawConn, hopAddr, clientConfig(res.user, auth, hkHop))
		if nerr != nil {
			_ = rawConn.Close()
			closeSSHStack(stack)
			return nil, fmt.Errorf("proxyjump handshake %q: %w", hopSpec, nerr)
		}
		next := ssh.NewClient(ncc, chans, reqs)
		stack = append(stack, next)
		cur = next
	}

	var leaf *ssh.Client
	finalAddr := net.JoinHostPort(final.host, strconv.Itoa(final.port))
	if len(jumps) == 0 {
		leaf, err = ssh.Dial("tcp", finalAddr, clientConfig(final.user, auth, hkFinal))
		if err != nil {
			return nil, err
		}
	} else {
		if cur == nil {
			return nil, fmt.Errorf("internal: proxyjump without first hop")
		}
		rawConn, derr := cur.Dial("tcp", finalAddr)
		if derr != nil {
			closeSSHStack(stack)
			return nil, fmt.Errorf("dial target via proxyjump: %w", derr)
		}
		ncc, chans, reqs, nerr := ssh.NewClientConn(rawConn, finalAddr, clientConfig(final.user, auth, hkFinal))
		if nerr != nil {
			_ = rawConn.Close()
			closeSSHStack(stack)
			return nil, fmt.Errorf("target ssh handshake: %w", nerr)
		}
		leaf = ssh.NewClient(ncc, chans, reqs)
	}

	gcfg := &goph.Config{
		User:     final.user,
		Addr:     final.host,
		Port:     uint(final.port),
		Auth:     auth,
		Timeout:  goph.DefaultTimeout,
		Callback: hkFinal,
	}
	return &HoneyClient{
		Client: &goph.Client{
			Client: leaf,
			Config: gcfg,
		},
		parents: stack,
	}, nil
}

// DialSSHClient returns the leaf *ssh.Client and a cleanup that closes the full ProxyJump chain.
func DialSSHClient(userOverride, hostAlias string) (*ssh.Client, func(), error) {
	h, err := DialHoneyClient(userOverride, hostAlias)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		_ = h.Close()
	}
	return h.Client.Client, cleanup, nil
}

func parseLocalForward(spec string) (localPort, remoteHost, remotePort string, err error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", "", "", fmt.Errorf("empty tunnel spec")
	}
	parts := strings.SplitN(spec, ":", 3)
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("tunnel spec must look like 8080:remotehost:8080")
	}
	localPort = strings.TrimSpace(parts[0])
	remoteHost = strings.TrimSpace(parts[1])
	remotePort = strings.TrimSpace(parts[2])
	if localPort == "" || remoteHost == "" || remotePort == "" {
		return "", "", "", fmt.Errorf("tunnel spec must look like 8080:remotehost:8080")
	}
	if _, err0 := strconv.Atoi(localPort); err0 != nil {
		return "", "", "", fmt.Errorf("local port: %w", err0)
	}
	if _, err0 := strconv.Atoi(remotePort); err0 != nil {
		return "", "", "", fmt.Errorf("remote port: %w", err0)
	}
	return localPort, remoteHost, remotePort, nil
}

// RunTunnelGo listens on 127.0.0.1:<localPort> and forwards to remoteHost:remotePort via the SSH server (host).
func RunTunnelGo(ctx context.Context, user, host, localFwd string, out io.Writer) error {
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("no IP for selected host")
	}
	localPort, remoteHost, remotePort, err := parseLocalForward(localFwd)
	if err != nil {
		return err
	}
	client, cleanup, err := DialSSHClient(user, host)
	if err != nil {
		return err
	}
	defer cleanup()

	bind := net.JoinHostPort("127.0.0.1", localPort)
	ln, err := net.Listen("tcp", bind)
	if err != nil {
		return fmt.Errorf("listen %s: %w", bind, err)
	}
	defer func() { _ = ln.Close() }()

	remoteAddr := net.JoinHostPort(remoteHost, remotePort)
	_, _ = fmt.Fprintf(os.Stderr, "Forwarding %s -> %s via SSH (Ctrl+C to stop)\n", bind, remoteAddr)

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, accErr := ln.Accept()
		if accErr != nil {
			if ctx.Err() != nil {
				return nil
			}
			return accErr
		}
		go forwardOneTunnel(client, conn, remoteAddr, out)
	}
}

func forwardOneTunnel(client *ssh.Client, local net.Conn, remoteAddr string, out io.Writer) {
	fmt.Fprintf(out, "[%s] Connection opened from %s\n", time.Now().Format(time.RFC3339), local.RemoteAddr())
	defer func() { fmt.Fprintf(out, "[%s] Connection closed from %s\n", time.Now().Format(time.RFC3339), local.RemoteAddr()) }()
	defer func() { _ = local.Close() }()
	remote, err := client.Dial("tcp", remoteAddr)
	if err != nil {
		_, _ = fmt.Fprintf(out, "tunnel dial %s: %v\n", remoteAddr, err)
		return
	}
	defer func() { _ = remote.Close() }()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(remote, local)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(local, remote)
	}()
	wg.Wait()
}
