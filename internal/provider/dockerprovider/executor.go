package dockerprovider

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sync/errgroup"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/safepath"
)

// InteractiveRunner runs an interactive TTY session against a Docker container.
// It is implemented in the ui package and injected via NewFactory to keep
// dockerprovider a leaf package (ui imports dockerprovider, not vice versa).
type InteractiveRunner interface {
	RunInteractive(user string, r hosts.Record, reg hostexec.Registry) error
}

// DockerExecutor implements hostexec.Executor for Docker containers.
type DockerExecutor struct {
	reg         hostexec.Registry
	interactive InteractiveRunner
}

// DockerNativeClient implements hostexec.HostClient via the Docker Engine API.
type DockerNativeClient struct {
	Cli         *client.Client
	ContainerID string
	ContainerOS string
}

// Dial connects to the Docker container and returns a DockerNativeClient.
func (e *DockerExecutor) Dial(user string, r hosts.Record) (hostexec.HostClient, error) {
	if !r.IsDocker() {
		return nil, fmt.Errorf("record is not a connectable docker container")
	}
	containerID, err := ContainerIDFromRecord(r.Meta["container_id"])
	if err != nil {
		return nil, err
	}
	cli, err := e.DialDockerClient(user, r)
	if err != nil {
		return nil, err
	}
	osLabel := strings.TrimSpace(r.Meta["container_platform"])
	if osLabel == "" {
		osLabel = "linux"
	}
	return &DockerNativeClient{Cli: cli, ContainerID: containerID, ContainerOS: osLabel}, nil
}

// RunInteractive delegates to the injected InteractiveRunner.
func (e *DockerExecutor) RunInteractive(user string, r hosts.Record) error {
	if e.interactive == nil {
		return fmt.Errorf("interactive session not configured")
	}
	return e.interactive.RunInteractive(user, r, e.reg)
}

// DockerExecutor satisfies the interactive-TTY seam so the web/CLI terminal
// paths can run a container shell through Registry.ForRecord without knowing the
// concrete client type.
var _ hostexec.InteractiveStreamer = (*DockerExecutor)(nil)

// RunInteractiveStreams runs an interactive container shell over the caller's
// streams (e.g. a web terminal's WebSocket pipes). It dials the container and
// drives DockerNativeClient.ExecInteractive; resize [cols, rows] pairs are
// adapted to the docker resize type.
func (e *DockerExecutor) RunInteractiveStreams(ctx context.Context, user string, r hosts.Record, stdin io.Reader, stdout io.Writer, cols, rows int, resize <-chan [2]int) error {
	hc, err := e.Dial(user, r)
	if err != nil {
		return err
	}
	defer func() { _ = hc.Close() }()
	dc, ok := hc.(*DockerNativeClient)
	if !ok {
		return fmt.Errorf("docker interactive: unexpected client type %T", hc)
	}
	execEnv, _ := cuetry.EnvForDockerInteractive(&r)
	return dc.ExecInteractive(ctx, DockerInteractiveShellCmd(dc), execEnv, stdin, stdout, cols, rows, colsRowsToDockerResize(ctx, resize))
}

// colsRowsToDockerResize adapts a neutral [cols, rows] resize stream to the
// docker resize type. The forwarding goroutine exits when the source channel
// closes or ctx is cancelled, so it never leaks.
func colsRowsToDockerResize(ctx context.Context, in <-chan [2]int) <-chan DockerTerminalSize {
	if in == nil {
		return nil
	}
	out := make(chan DockerTerminalSize)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case sz, ok := <-in:
				if !ok {
					return
				}
				select {
				case out <- DockerTerminalSize{Cols: sz[0], Rows: sz[1]}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

// DialDockerCheck verifies that a docker record can reach the Engine API (dial + close).
func DialDockerCheck(user string, r hosts.Record, reg hostexec.Registry) error {
	ex := &DockerExecutor{reg: reg}
	c, err := ex.Dial(user, r)
	if err != nil {
		return err
	}
	return c.Close()
}

// RunTunnel is not supported for Docker containers.
func (e *DockerExecutor) RunTunnel(context.Context, string, hosts.Record, string, io.Writer) error {
	return fmt.Errorf("docker provider does not support SSH-style tunnels; publish ports on the container instead")
}

// DialUpstream connects to a port inside the container via nc/socat.
func (e *DockerExecutor) DialUpstream(_ context.Context, user string, r hosts.Record, address string) (net.Conn, error) {
	if pt, perr := hostexec.ParseTunnelTarget(address); perr == nil && pt.Scheme == hostexec.TunnelUnix {
		return nil, fmt.Errorf("unix socket target not supported by docker backend")
	}
	c, err := e.Dial(user, r)
	if err != nil {
		return nil, err
	}

	host, port, err := net.SplitHostPort(address)
	if err != nil {
		host = address
		port = "80"
	}

	p1, p2 := net.Pipe()

	cmd := fmt.Sprintf("nc %s %s || socat STDIO TCP:%s:%s", host, port, host, port)

	go func() {
		defer func() { _ = c.Close() }()
		defer func() { _ = p1.Close() }()
		_ = c.RunWithStreams(cmd, p1, p1, nil)
	}()

	return p2, nil
}

// EffectiveDockerSSHUser returns the SSH user for docker transport resolution.
func EffectiveDockerSSHUser(user string, r hosts.Record) string {
	if u := strings.TrimSpace(user); u != "" {
		return u
	}
	return strings.TrimSpace(r.Meta["docker_ssh_user"])
}

// DialDockerClient builds a moby client from record metadata (transport resolution).
func (e *DockerExecutor) DialDockerClient(user string, r hosts.Record) (*client.Client, error) {
	user = EffectiveDockerSSHUser(user, r)
	opts := APIClientOptions{SSHUser: user}
	transport := strings.TrimSpace(r.Meta["docker_transport"])
	host := strings.TrimSpace(r.Meta["docker_host"])
	backend := strings.TrimSpace(r.Meta["docker_backend"])

	if transport == "honey_ssh" || strings.HasPrefix(host, "honey-ssh://") {
		// Auto-discover rows leave docker_backend empty; DockerBackendByName("")
		// would match the first YAML backends.docker entry (often local DOCKER_HOST).
		// Prefer the VM hop whenever discover metadata is present.
		if vm, ok := VMRecordForHoneyDocker(r); ok {
			if e.reg != nil {
				if s, ok := e.reg.BorrowSSH(opts.SSHUser, vm); ok {
					if sshClient, ok := s.(*ssh.Client); ok {
						opts.BorrowedSSH = sshClient
					}
				}
			}
			bc := BackendConfig{
				SSHUser: user,
				RunAs:   strings.TrimSpace(r.Meta["docker_run_as"]),
			}
			opts.VMRecord = &vm
			return NewAPIClient(context.Background(), bc, opts)
		}
		if backend != "" {
			if rt, ok := BackendByName(backend); ok {
				return NewAPIClientFromRuntime(context.Background(), rt, opts)
			}
		}
		return nil, fmt.Errorf("honey-ssh docker record missing backend or vm metadata")
	}

	if rt, ok := BackendByName(backend); ok {
		return NewAPIClientFromRuntime(context.Background(), rt, opts)
	}
	if host == "" || host == "env" {
		return NewAPIClient(context.Background(), BackendConfig{}, opts)
	}
	return NewAPIClient(context.Background(), BackendConfig{Host: host}, opts)
}

// VMRecordForHoneyDocker extracts a VM hop record from docker record metadata.
func VMRecordForHoneyDocker(r hosts.Record) (hosts.Record, bool) {
	ip := strings.TrimSpace(r.Meta["docker_vm_ip"])
	if ip == "" {
		if h := strings.TrimSpace(r.Meta["docker_host"]); strings.HasPrefix(h, "honey-ssh://") {
			rest := strings.TrimPrefix(h, "honey-ssh://")
			if at := strings.LastIndex(rest, "@"); at >= 0 {
				ip = rest[at+1:]
			} else {
				ip = rest
			}
		}
	}
	if ip == "" {
		return hosts.Record{}, false
	}
	meta := map[string]string{}
	if p := strings.TrimSpace(r.Meta["ssh_port"]); p != "" {
		meta["ssh_port"] = p
	}
	if id := strings.TrimSpace(r.Meta["ssh_identity_file"]); id != "" {
		meta["ssh_identity_file"] = id
	}
	return hosts.Record{
		Provider:  strings.TrimSpace(r.Meta["via_provider"]),
		Name:      strings.TrimSpace(r.Meta["docker_vm"]),
		PrimaryIP: ip,
		Meta:      meta,
	}, true
}

// Close closes the underlying Docker client connection.
func (c *DockerNativeClient) Close() error {
	if c.Cli != nil {
		return c.Cli.Close()
	}
	return nil
}

func (c *DockerNativeClient) execInContainer(ctx context.Context, cmd []string, stdin io.Reader, stdout, stderr io.Writer, tty bool) error {
	if stderr == nil {
		stderr = io.Discard
	}
	execCfg := client.ExecCreateOptions{
		AttachStdin:  stdin != nil,
		AttachStdout: stdout != nil,
		AttachStderr: true,
		TTY:          tty,
		Cmd:          cmd,
	}
	execID, err := c.Cli.ExecCreate(ctx, c.ContainerID, execCfg)
	if err != nil {
		return err
	}
	attach, err := c.Cli.ExecAttach(ctx, execID.ID, client.ExecAttachOptions{TTY: tty})
	if err != nil {
		return err
	}
	defer attach.Close()

	if tty {
		_, err = io.Copy(stdout, attach.Reader)
		return err
	}
	_, err = stdcopy.StdCopy(stdout, stderr, attach.Reader)
	return err
}

// DockerInteractiveShellCmd returns the shell command for an interactive session.
func DockerInteractiveShellCmd(dc *DockerNativeClient) []string {
	if dc.IsWindowsContainer() {
		return []string{"powershell.exe", "-NoLogo"}
	}
	return []string{"sh"}
}

// cancelOnContext wraps r so Read returns io.EOF when ctx is canceled (unblocks TTY after remote shell exits).
type cancelOnContext struct {
	ctx context.Context
	r   io.Reader
}

func (c *cancelOnContext) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, io.EOF
	}
	type readResult struct {
		n   int
		err error
	}
	ch := make(chan readResult, 1)
	go func() {
		n, err := c.r.Read(p)
		ch <- readResult{n, err}
	}()
	select {
	case <-c.ctx.Done():
		return 0, io.EOF
	case res := <-ch:
		return res.n, res.err
	}
}

func benignExecStreamErr(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context canceled") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "connection reset")
}

// DockerTerminalSize is a cols/rows pair for docker exec resize.
type DockerTerminalSize struct {
	Cols int
	Rows int
}

// ExecInteractive runs a TTY exec with bidirectional I/O and optional resize events.
func (c *DockerNativeClient) ExecInteractive(
	ctx context.Context,
	cmd []string,
	execEnv []string,
	stdin io.Reader,
	stdout io.Writer,
	cols, rows int,
	resizeCh <-chan DockerTerminalSize,
) error {
	if stdout == nil {
		stdout = io.Discard
	}
	created, err := c.Cli.ExecCreate(ctx, c.ContainerID, client.ExecCreateOptions{
		AttachStdin:  stdin != nil,
		AttachStdout: true,
		AttachStderr: false,
		TTY:          true,
		Cmd:          cmd,
		Env:          execEnv,
	})
	if err != nil {
		return err
	}
	execID := created.ID

	attach, err := c.Cli.ExecAttach(ctx, execID, client.ExecAttachOptions{TTY: true})
	if err != nil {
		return err
	}
	defer attach.Close()

	doResize := func(width, height uint) {
		_, _ = c.Cli.ExecResize(ctx, execID, client.ExecResizeOptions{
			Width:  width,
			Height: height,
		})
	}
	if cols > 0 && rows > 0 {
		doResize(uint(cols), uint(rows))
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				inspect, ierr := c.Cli.ExecInspect(ctx, execID, client.ExecInspectOptions{})
				if ierr == nil && !inspect.Running {
					cancel()
					return
				}
			}
		}
	}()

	if resizeCh != nil {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case sz, ok := <-resizeCh:
					if !ok {
						return
					}
					if sz.Cols > 0 && sz.Rows > 0 {
						doResize(uint(sz.Cols), uint(sz.Rows))
					}
				}
			}
		}()
	}

	g, _ := errgroup.WithContext(ctx)
	if stdin != nil {
		stdinCtx := &cancelOnContext{ctx: ctx, r: stdin}
		g.Go(func() error {
			_, copyErr := io.Copy(attach.Conn, stdinCtx)
			if cw, ok := attach.Conn.(interface{ CloseWrite() error }); ok {
				_ = cw.CloseWrite()
			}
			if copyErr != nil && copyErr != io.EOF && !benignExecStreamErr(copyErr) {
				return copyErr
			}
			return nil
		})
	}
	g.Go(func() error {
		_, copyErr := io.Copy(stdout, attach.Reader)
		cancel()
		return copyErr
	})
	streamErr := g.Wait()

	inspect, ierr := c.Cli.ExecInspect(context.Background(), execID, client.ExecInspectOptions{})
	if ierr != nil {
		return ierr
	}
	if inspect.ExitCode == 0 || benignExecStreamErr(streamErr) {
		return nil
	}
	if streamErr != nil {
		return streamErr
	}
	return fmt.Errorf("exec exited with code %d", inspect.ExitCode)
}

// IsWindowsContainer reports whether the container runs Windows.
func (c *DockerNativeClient) IsWindowsContainer() bool {
	return strings.EqualFold(strings.TrimSpace(c.ContainerOS), "windows")
}

func (c *DockerNativeClient) execArgv(cmd string) []string {
	if c.IsWindowsContainer() {
		return []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command", cmd}
	}
	return []string{"sh", "-c", cmd}
}

// Run executes cmd in the container and returns combined stdout.
func (c *DockerNativeClient) Run(cmd string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	err := c.execInContainer(context.Background(), c.execArgv(cmd), nil, &stdout, &stderr, false)
	if err != nil {
		if stderr.Len() > 0 {
			return stdout.Bytes(), fmt.Errorf("%w: %s", err, stderr.String())
		}
		return stdout.Bytes(), err
	}
	return stdout.Bytes(), nil
}

// RunWithStreams executes cmd in the container with the provided I/O streams.
func (c *DockerNativeClient) RunWithStreams(cmd string, stdin io.Reader, stdout, stderr io.Writer) error {
	if stderr == nil {
		stderr = io.Discard
	}
	return c.execInContainer(context.Background(), c.execArgv(cmd), stdin, stdout, stderr, false)
}

// Upload copies a local file into the container at remotePath.
func (c *DockerNativeClient) Upload(localPath, remotePath string) error {
	localPath = strings.TrimSpace(localPath)
	remotePath = strings.TrimSpace(remotePath)
	if localPath == "" || remotePath == "" {
		return fmt.Errorf("upload: empty local or remote path")
	}
	if strings.HasSuffix(remotePath, "/") {
		base := filepath.Base(localPath)
		if base == "" || base == "." {
			return fmt.Errorf("upload: need file name in remote directory %q", remotePath)
		}
		remotePath = path.Join(strings.TrimRight(remotePath, "/"), base)
	}

	localFile, err := safepath.Open(localPath)
	if err != nil {
		return err
	}
	defer localFile.Close()

	stat, err := localFile.Stat()
	if err != nil {
		return err
	}

	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		tw := tar.NewWriter(pw)
		defer tw.Close()
		hdr := &tar.Header{
			Name: path.Base(remotePath),
			Mode: int64(stat.Mode()),
			Size: stat.Size(),
		}
		_ = tw.WriteHeader(hdr)
		_, _ = io.Copy(tw, localFile)
	}()

	_, err = c.Cli.CopyToContainer(context.Background(), c.ContainerID, client.CopyToContainerOptions{
		DestinationPath:           path.Dir(remotePath),
		Content:                   pr,
		AllowOverwriteDirWithFile: true,
	})
	return err
}

// maxDockerTarFileBytes caps bytes read from a single tar entry when copying from a container.
const maxDockerTarFileBytes int64 = 1 << 30 // 1 GiB

func tarHeaderFileMode(mode int64) os.FileMode {
	const (
		defaultMode os.FileMode = 0o644
		permMask    int64       = 0o7777
	)
	m := mode & permMask
	if m <= 0 {
		return defaultMode
	}
	return os.FileMode(uint32(m)) //nolint:gosec // G115: masked to permission bits within uint32 range
}

func copyTarFileContent(dst io.Writer, tr io.Reader, size int64) (int64, error) {
	limit := maxDockerTarFileBytes
	if size >= 0 {
		if size > maxDockerTarFileBytes {
			return 0, fmt.Errorf("download: file too large (%d bytes)", size)
		}
		limit = size
	}
	return io.Copy(dst, io.LimitReader(tr, limit))
}

// Download copies a file from the container to localPath.
func (c *DockerNativeClient) Download(remotePath, localPath string) error {
	remotePath = strings.TrimSpace(remotePath)
	if remotePath == "" {
		return fmt.Errorf("download: empty remote path")
	}
	copyResult, err := c.Cli.CopyFromContainer(context.Background(), c.ContainerID, client.CopyFromContainerOptions{
		SourcePath: remotePath,
	})
	if err != nil {
		return err
	}
	reader := copyResult.Content
	defer reader.Close()

	tr := tar.NewReader(reader)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		f, err := safepath.OpenFile(localPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, tarHeaderFileMode(hdr.Mode))
		if err != nil {
			return err
		}
		_, cpErr := copyTarFileContent(f, tr, hdr.Size)
		closeErr := f.Close()
		if cpErr != nil {
			return cpErr
		}
		return closeErr
	}
	return fmt.Errorf("file not found in container archive: %s", remotePath)
}

// DockerShellQuote returns a shell-safe single-quoted form of s.
func DockerShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ListRemoteDir lists files in dir inside the container.
func (c *DockerNativeClient) ListRemoteDir(dir string) ([]hostexec.RemoteFileEntry, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		if c.IsWindowsContainer() {
			dir = `C:\`
		} else {
			dir = "/"
		}
	}
	if c.IsWindowsContainer() {
		return c.listRemoteDirWindows(dir)
	}
	dir = path.Clean(dir)
	script := fmt.Sprintf(`find %s -mindepth 1 -maxdepth 1 -exec stat -c '%%n|%%F|%%s|%%Y' {} \;`, DockerShellQuote(dir))
	out, err := c.Run(script)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	entries := make([]hostexec.RemoteFileEntry, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 {
			continue
		}
		fullPath := parts[0]
		typeField := parts[1]
		size, _ := strconv.ParseInt(parts[2], 10, 64)
		mtimeUnix, _ := strconv.ParseInt(parts[3], 10, 64)
		entries = append(entries, hostexec.RemoteFileEntry{
			Name:       path.Base(fullPath),
			Path:       fullPath,
			IsDir:      strings.Contains(typeField, "directory"),
			Size:       size,
			Mode:       typeField,
			ModifiedAt: time.Unix(mtimeUnix, 0),
		})
	}
	return entries, nil
}

func (c *DockerNativeClient) listRemoteDirWindows(dir string) ([]hostexec.RemoteFileEntry, error) {
	dir = strings.ReplaceAll(dir, "'", "''")
	script := fmt.Sprintf(
		`Get-ChildItem -LiteralPath '%s' | ForEach-Object { $_.FullName + '|' + $(if ($_.PSIsContainer) {'directory'} else {'file'}) + '|' + $_.Length + '|' + [int][double]::Parse(($_.LastWriteTimeUtc - [datetime]'1970-01-01').TotalSeconds) }`,
		dir,
	)
	out, err := c.Run(script)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	entries := make([]hostexec.RemoteFileEntry, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 {
			continue
		}
		size, _ := strconv.ParseInt(parts[2], 10, 64)
		mtimeUnix, _ := strconv.ParseInt(parts[3], 10, 64)
		entries = append(entries, hostexec.RemoteFileEntry{
			Name:       filepath.Base(parts[0]),
			Path:       parts[0],
			IsDir:      strings.Contains(parts[1], "directory"),
			Size:       size,
			Mode:       parts[1],
			ModifiedAt: time.Unix(mtimeUnix, 0),
		})
	}
	return entries, nil
}

// StatRemote returns metadata for remotePath inside the container.
func (c *DockerNativeClient) StatRemote(remotePath string) (hostexec.RemoteFileEntry, error) {
	remotePath = strings.TrimSpace(remotePath)
	if remotePath == "" {
		return hostexec.RemoteFileEntry{}, fmt.Errorf("empty path")
	}
	stResult, err := c.Cli.ContainerStatPath(context.Background(), c.ContainerID, client.ContainerStatPathOptions{
		Path: remotePath,
	})
	if err != nil {
		return hostexec.RemoteFileEntry{}, err
	}
	st := stResult.Stat
	return hostexec.RemoteFileEntry{
		Name:       path.Base(remotePath),
		Path:       remotePath,
		IsDir:      st.Mode.IsDir(),
		Size:       st.Size,
		Mode:       st.Mode.String(),
		ModifiedAt: st.Mtime,
	}, nil
}

// MkdirAllRemote creates remotePath (and parents) inside the container.
func (c *DockerNativeClient) MkdirAllRemote(remotePath string) error {
	remotePath = strings.TrimSpace(remotePath)
	if remotePath == "" {
		return fmt.Errorf("empty path")
	}
	var cmd string
	if c.IsWindowsContainer() {
		q := strings.ReplaceAll(remotePath, "'", "''")
		cmd = fmt.Sprintf(`New-Item -ItemType Directory -Force -Path '%s' | Out-Null`, q)
	} else {
		cmd = fmt.Sprintf("mkdir -p %s", DockerShellQuote(remotePath))
	}
	_, err := c.Run(cmd)
	return err
}

// RemoveRemote deletes remotePath inside the container; recursive removes directories.
func (c *DockerNativeClient) RemoveRemote(remotePath string, recursive bool) error {
	remotePath = strings.TrimSpace(remotePath)
	if remotePath == "" {
		return fmt.Errorf("empty path")
	}
	var cmd string
	switch {
	case c.IsWindowsContainer() && recursive:
		q := strings.ReplaceAll(remotePath, "'", "''")
		cmd = fmt.Sprintf(`Remove-Item -LiteralPath '%s' -Recurse -Force`, q)
	case c.IsWindowsContainer():
		q := strings.ReplaceAll(remotePath, "'", "''")
		cmd = fmt.Sprintf(`Remove-Item -LiteralPath '%s' -Force`, q)
	case recursive:
		cmd = fmt.Sprintf("rm -rf %s", DockerShellQuote(remotePath))
	default:
		cmd = fmt.Sprintf("rm -f %s", DockerShellQuote(remotePath))
	}
	_, err := c.Run(cmd)
	return err
}

// StartLocalForward starts a local port forward.
func (c *DockerNativeClient) StartLocalForward(_ context.Context, _ string, _ int, _ string, _ int) (host string, port int, stop func(), err error) {
	return "", 0, nil, fmt.Errorf("tunneling not supported on this transport")
}

// StartRemoteForward starts a remote port forward.
func (c *DockerNativeClient) StartRemoteForward(_ context.Context, _ string, _ int, _ string, _ int) (remAddr string, stop func(), err error) {
	return "", nil, fmt.Errorf("tunneling not supported on this transport")
}

// StartDynamicForward starts a dynamic port forward.
func (c *DockerNativeClient) StartDynamicForward(_ context.Context, _ string, _ int) (host string, port int, stop func(), err error) {
	return "", 0, nil, fmt.Errorf("tunneling not supported on this transport")
}

// StartUDPRelay starts a UDP relay.
func (c *DockerNativeClient) StartUDPRelay(_ context.Context, _ string, _ int, _ string, _ int, _ bool) (host string, port int, stop func(), err error) {
	return "", 0, nil, fmt.Errorf("tunneling not supported on this transport")
}

// StartTunForward starts a TUN forward.
func (c *DockerNativeClient) StartTunForward(_ context.Context, _ string, _ string, _ int, _, _ int) (tunName string, stop func(), err error) {
	return "", nil, fmt.Errorf("tunneling not supported on this transport")
}

// StartLocalSocketForward starts a local unix-socket forward.
func (c *DockerNativeClient) StartLocalSocketForward(_ context.Context, _ string, _ string) (localPath string, stop func(), err error) {
	return "", nil, fmt.Errorf("unix socket forward not supported by docker backend")
}
