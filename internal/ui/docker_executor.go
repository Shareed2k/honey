package ui

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
	"golang.org/x/sync/errgroup"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/dockerprovider"
)

type dockerExecutor struct{}

type dockerNativeClient struct {
	cli         *client.Client
	containerID string
	containerOS string
}

func (dockerExecutor) Dial(user string, r hosts.Record) (HostClient, error) {
	if !hosts.IsDockerRecord(r) {
		return nil, fmt.Errorf("record is not a connectable docker container")
	}
	containerID, err := dockerprovider.ContainerIDFromRecord(r.Meta["container_id"])
	if err != nil {
		return nil, err
	}
	cli, err := dialDockerClient(user, r)
	if err != nil {
		return nil, err
	}
	osLabel := strings.TrimSpace(r.Meta["container_platform"])
	if osLabel == "" {
		osLabel = "linux"
	}
	return &dockerNativeClient{cli: cli, containerID: containerID, containerOS: osLabel}, nil
}

func (dockerExecutor) RunInteractive(user string, r hosts.Record) error {
	return runDockerInteractiveWithRecorder(user, r, nil)
}

func (dockerExecutor) RunTunnel(context.Context, string, hosts.Record, string, io.Writer) error {
	return fmt.Errorf("docker provider does not support SSH-style tunnels; publish ports on the container instead")
}

func dialDockerClient(user string, r hosts.Record) (*client.Client, error) {
	opts := dockerprovider.APIClientOptions{SSHUser: user}
	transport := strings.TrimSpace(r.Meta["docker_transport"])
	host := strings.TrimSpace(r.Meta["docker_host"])
	backend := strings.TrimSpace(r.Meta["docker_backend"])

	if transport == "honey_ssh" || strings.HasPrefix(host, "honey-ssh://") {
		if rt, ok := hostexec.DockerBackendByName(backend); ok {
			return dockerprovider.NewAPIClientFromRuntime(context.Background(), rt, opts)
		}
		if vm, ok := vmRecordForHoneyDocker(r); ok {
			bc := dockerprovider.BackendConfig{
				SSHUser: user,
				RunAs:   strings.TrimSpace(r.Meta["docker_run_as"]),
			}
			opts.VMRecord = &vm
			return dockerprovider.NewAPIClient(context.Background(), bc, opts)
		}
		return nil, fmt.Errorf("honey-ssh docker record missing backend or vm metadata")
	}

	if rt, ok := hostexec.DockerBackendByName(backend); ok {
		return dockerprovider.NewAPIClientFromRuntime(context.Background(), rt, opts)
	}
	if host == "" || host == "env" {
		return dockerprovider.NewAPIClient(context.Background(), dockerprovider.BackendConfig{}, opts)
	}
	return dockerprovider.NewAPIClient(context.Background(), dockerprovider.BackendConfig{Host: host}, opts)
}

func vmRecordForHoneyDocker(r hosts.Record) (hosts.Record, bool) {
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

func (c *dockerNativeClient) Close() error {
	if c.cli != nil {
		return c.cli.Close()
	}
	return nil
}

func (c *dockerNativeClient) execInContainer(ctx context.Context, cmd []string, stdin io.Reader, stdout, stderr io.Writer, tty bool) error {
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
	execID, err := c.cli.ExecCreate(ctx, c.containerID, execCfg)
	if err != nil {
		return err
	}
	attach, err := c.cli.ExecAttach(ctx, execID.ID, client.ExecAttachOptions{TTY: tty})
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

// execInteractive runs a TTY exec with bidirectional I/O and optional resize events.
func (c *dockerNativeClient) execInteractive(
	ctx context.Context,
	cmd []string,
	stdin io.Reader,
	stdout io.Writer,
	cols, rows int,
	resizeCh <-chan DockerTerminalSize,
) error {
	if stdout == nil {
		stdout = io.Discard
	}
	created, err := c.cli.ExecCreate(ctx, c.containerID, client.ExecCreateOptions{
		AttachStdin:  stdin != nil,
		AttachStdout: true,
		AttachStderr: false,
		TTY:          true,
		Cmd:          cmd,
	})
	if err != nil {
		return err
	}
	execID := created.ID

	attach, err := c.cli.ExecAttach(ctx, execID, client.ExecAttachOptions{TTY: true})
	if err != nil {
		return err
	}
	defer attach.Close()

	doResize := func(width, height uint) {
		_, _ = c.cli.ExecResize(ctx, execID, client.ExecResizeOptions{
			Width:  width,
			Height: height,
		})
	}
	if cols > 0 && rows > 0 {
		doResize(uint(cols), uint(rows))
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

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

	g, gctx := errgroup.WithContext(ctx)
	if stdin != nil {
		g.Go(func() error {
			_, copyErr := io.Copy(attach.Conn, stdin)
			if cw, ok := attach.Conn.(interface{ CloseWrite() error }); ok {
				_ = cw.CloseWrite()
			}
			if copyErr != nil && copyErr != io.EOF {
				return copyErr
			}
			return nil
		})
	}
	g.Go(func() error {
		_, copyErr := io.Copy(stdout, attach.Reader)
		return copyErr
	})
	streamErr := g.Wait()

	for {
		if gctx.Err() != nil {
			return gctx.Err()
		}
		inspect, ierr := c.cli.ExecInspect(ctx, execID, client.ExecInspectOptions{})
		if ierr != nil {
			return ierr
		}
		if !inspect.Running {
			if inspect.ExitCode != 0 && streamErr == nil {
				return fmt.Errorf("exec exited with code %d", inspect.ExitCode)
			}
			return streamErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (c *dockerNativeClient) isWindowsContainer() bool {
	return strings.EqualFold(strings.TrimSpace(c.containerOS), "windows")
}

func (c *dockerNativeClient) execArgv(cmd string) []string {
	if c.isWindowsContainer() {
		return []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command", cmd}
	}
	return []string{"sh", "-c", cmd}
}

func (c *dockerNativeClient) Run(cmd string) ([]byte, error) {
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

func (c *dockerNativeClient) RunWithStreams(cmd string, stdin io.Reader, stdout, stderr io.Writer) error {
	if stderr == nil {
		stderr = io.Discard
	}
	return c.execInContainer(context.Background(), c.execArgv(cmd), stdin, stdout, stderr, false)
}

func (c *dockerNativeClient) Upload(localPath, remotePath string) error {
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

	localFile, err := os.Open(localPath) // #nosec G304
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

	_, err = c.cli.CopyToContainer(context.Background(), c.containerID, client.CopyToContainerOptions{
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

func (c *dockerNativeClient) Download(remotePath, localPath string) error {
	remotePath = strings.TrimSpace(remotePath)
	if remotePath == "" {
		return fmt.Errorf("download: empty remote path")
	}
	copyResult, err := c.cli.CopyFromContainer(context.Background(), c.containerID, client.CopyFromContainerOptions{
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
		f, err := os.OpenFile(localPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, tarHeaderFileMode(hdr.Mode)) // #nosec G304
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

func (c *dockerNativeClient) ListRemoteDir(dir string) ([]RemoteFileEntry, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		if c.isWindowsContainer() {
			dir = `C:\`
		} else {
			dir = "/"
		}
	}
	if c.isWindowsContainer() {
		return c.listRemoteDirWindows(dir)
	}
	dir = path.Clean(dir)
	script := fmt.Sprintf(`find %s -mindepth 1 -maxdepth 1 -exec stat -c '%%n|%%F|%%s|%%Y' {} \;`, dockerShellQuote(dir))
	out, err := c.Run(script)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	entries := make([]RemoteFileEntry, 0, len(lines))
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
		entries = append(entries, RemoteFileEntry{
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

func (c *dockerNativeClient) listRemoteDirWindows(dir string) ([]RemoteFileEntry, error) {
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
	entries := make([]RemoteFileEntry, 0, len(lines))
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
		entries = append(entries, RemoteFileEntry{
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

func (c *dockerNativeClient) StatRemote(remotePath string) (RemoteFileEntry, error) {
	remotePath = strings.TrimSpace(remotePath)
	if remotePath == "" {
		return RemoteFileEntry{}, fmt.Errorf("empty path")
	}
	stResult, err := c.cli.ContainerStatPath(context.Background(), c.containerID, client.ContainerStatPathOptions{
		Path: remotePath,
	})
	if err != nil {
		return RemoteFileEntry{}, err
	}
	st := stResult.Stat
	return RemoteFileEntry{
		Name:       path.Base(remotePath),
		Path:       remotePath,
		IsDir:      st.Mode.IsDir(),
		Size:       st.Size,
		Mode:       st.Mode.String(),
		ModifiedAt: st.Mtime,
	}, nil
}

func (c *dockerNativeClient) MkdirAllRemote(remotePath string) error {
	remotePath = strings.TrimSpace(remotePath)
	if remotePath == "" {
		return fmt.Errorf("empty path")
	}
	var cmd string
	if c.isWindowsContainer() {
		q := strings.ReplaceAll(remotePath, "'", "''")
		cmd = fmt.Sprintf(`New-Item -ItemType Directory -Force -Path '%s' | Out-Null`, q)
	} else {
		cmd = fmt.Sprintf("mkdir -p %s", dockerShellQuote(remotePath))
	}
	_, err := c.Run(cmd)
	return err
}

func (c *dockerNativeClient) RemoveRemote(remotePath string, recursive bool) error {
	remotePath = strings.TrimSpace(remotePath)
	if remotePath == "" {
		return fmt.Errorf("empty path")
	}
	var cmd string
	switch {
	case c.isWindowsContainer() && recursive:
		q := strings.ReplaceAll(remotePath, "'", "''")
		cmd = fmt.Sprintf(`Remove-Item -LiteralPath '%s' -Recurse -Force`, q)
	case c.isWindowsContainer():
		q := strings.ReplaceAll(remotePath, "'", "''")
		cmd = fmt.Sprintf(`Remove-Item -LiteralPath '%s' -Force`, q)
	case recursive:
		cmd = fmt.Sprintf("rm -rf %s", dockerShellQuote(remotePath))
	default:
		cmd = fmt.Sprintf("rm -f %s", dockerShellQuote(remotePath))
	}
	_, err := c.Run(cmd)
	return err
}

func dockerShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func runDockerInteractiveWithRecorder(_ string, r hosts.Record, recorder *SessionRecorder) error {
	client, err := dockerExecutor{}.Dial("", r)
	if err != nil {
		if recorder != nil {
			recorder.RecordError(err)
		}
		return err
	}
	defer func() { _ = client.Close() }()

	dc, ok := client.(*dockerNativeClient)
	if !ok {
		err := fmt.Errorf("unexpected client type %T", client)
		if recorder != nil {
			recorder.RecordError(err)
		}
		return err
	}

	fd := int(os.Stdin.Fd())
	if !termIsTerminal(fd) {
		err := fmt.Errorf("stdin is not a terminal")
		if recorder != nil {
			recorder.RecordError(err)
		}
		return err
	}
	oldState, err := termMakeRaw(fd)
	if err != nil {
		if recorder != nil {
			recorder.RecordError(err)
		}
		return err
	}
	defer func() { _ = termRestore(fd, oldState) }()

	env, _ := cuetry.EffectiveEnvForRun(context.Background(), false, nil, cuetry.RecipeStep{}, nil, nil, &r)
	shell := "sh"
	if dc.isWindowsContainer() {
		shell = "powershell.exe"
	}
	cmd, _ := cuetry.ShellExportPrefixForRemote(env, shell)

	var stdin io.Reader = os.Stdin
	var stdout io.Writer = os.Stdout
	if recorder != nil {
		stdin = WrapRecordingReader(os.Stdin, recorder, "stdin")
		stdout = WrapRecordingWriter(os.Stdout, recorder, "stdout")
	}
	execErr := dc.execInteractive(context.Background(), dc.execArgv(cmd), stdin, stdout, 0, 0, nil)
	if execErr != nil && recorder != nil {
		recorder.RecordError(execErr)
	}
	return execErr
}
