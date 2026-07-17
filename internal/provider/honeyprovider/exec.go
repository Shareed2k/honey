package honeyprovider

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/shareed2k/honey/internal/devmtls"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/safepath"
)

// clientTLSConfig builds the TLS config for a honey upstream connection: the
// device mTLS client credential when mtls, else optional insecure server TLS.
func clientTLSConfig(insecure, mtls bool, serverCA string) (*tls.Config, error) {
	if mtls {
		return devmtls.ClientTLSConfig(serverCA)
	}
	return &tls.Config{InsecureSkipVerify: insecure}, nil // #nosec G402 -- insecure is operator opt-in
}

// Executor implements the hostexec.Executor interface to proxy connections through an upstream Honey server.
type Executor struct {
	URL      string
	Token    string
	Insecure bool
	MTLS     bool
	ServerCA string
	Mesh     bool
	MeshAddr string
}

// Dial creates a new HostClient that proxies execution to the upstream Honey server.
func (e *Executor) Dial(user string, r hosts.Record) (hostexec.HostClient, error) {
	return &Client{
		url:      e.URL,
		token:    e.Token,
		insecure: e.Insecure,
		mtls:     e.MTLS,
		serverCA: e.ServerCA,
		mesh:     e.Mesh,
		meshAddr: e.MeshAddr,
		user:     user,
		record:   r,
	}, nil
}

// RunInteractive is not currently implemented for proxy proxying.
func (e *Executor) RunInteractive(_ string, _ hosts.Record) error {
	return fmt.Errorf("interactive proxying via honey upstream is not yet implemented")
}

// RunTunnel runs a local port forward via the upstream Honey proxy.
func (e *Executor) RunTunnel(ctx context.Context, user string, r hosts.Record, localFwd string, out io.Writer) error {
	parts := strings.Split(localFwd, ":")
	var lp, rh, rp string
	switch len(parts) {
	case 3:
		lp, rh, rp = parts[0], parts[1], parts[2]
	case 2:
		lp, rh, rp = parts[0], "127.0.0.1", parts[1]
	default:
		return fmt.Errorf("invalid local forward format, expected [bind_address:]port:host:hostport")
	}

	listener, err := net.Listen("tcp", "127.0.0.1:"+lp)
	if err != nil {
		return err
	}
	defer listener.Close()

	if out != nil {
		_, _ = fmt.Fprintf(out, "Forwarding %s -> %s:%s via Honey proxy\n", listener.Addr().String(), rh, rp)
	}

	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}

		go func(local net.Conn) {
			defer local.Close()
			remote, err := e.DialUpstream(ctx, user, r, rh+":"+rp)
			if err != nil {
				return
			}
			defer remote.Close()

			errc := make(chan error, 2)
			go func() {
				_, err := io.Copy(remote, local)
				errc <- err
			}()
			go func() {
				_, err := io.Copy(local, remote)
				errc <- err
			}()
			<-errc
		}(conn)
	}
}

// DialUpstream dials a remote address via the upstream Honey proxy.
func (e *Executor) DialUpstream(ctx context.Context, user string, r hosts.Record, address string) (net.Conn, error) {
	wsURL := strings.Replace(e.URL, "http", "ws", 1) + "/api/v1/ws/tunnel"
	tlsCfg, err := clientTLSConfig(e.Insecure, e.MTLS, e.ServerCA)
	if err != nil {
		return nil, err
	}
	token := e.Token
	if e.MTLS {
		token = ""
	}
	conn, err := dialWS(ctx, wsURL, token, tlsCfg, meshDialContext(e.Mesh, e.MeshAddr))
	if err != nil {
		return nil, err
	}

	hello := map[string]any{"ssh_user": user, "record": r, "target": address}
	if err := conn.WriteJSON(hello); err != nil {
		conn.Close()
		return nil, err
	}

	var resp map[string]any
	if err := conn.ReadJSON(&resp); err != nil {
		conn.Close()
		return nil, err
	}
	if errStr, ok := resp["error"].(string); ok && errStr != "" {
		conn.Close()
		return nil, fmt.Errorf("upstream dial error: %s", errStr)
	}

	return &wsConn{conn: conn}, nil
}

func dialWS(ctx context.Context, u, token string, tlsCfg *tls.Config, dialCtx func(context.Context, string, string) (net.Conn, error)) (*websocket.Conn, error) {
	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 15 * time.Second,
		TLSClientConfig:  tlsCfg,
	}
	if dialCtx != nil {
		dialer.NetDialContext = dialCtx
	}
	headers := http.Header{}
	if token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}
	conn, _, err := dialer.DialContext(ctx, u, headers)
	return conn, err
}

type wsConn struct {
	conn *websocket.Conn
	buf  []byte
}

func (c *wsConn) Read(b []byte) (n int, err error) {
	if len(c.buf) > 0 {
		n = copy(b, c.buf)
		c.buf = c.buf[n:]
		return n, nil
	}
	_, p, err := c.conn.ReadMessage()
	if err != nil {
		return 0, err
	}
	n = copy(b, p)
	if n < len(p) {
		c.buf = p[n:]
	}
	return n, nil
}

func (c *wsConn) Write(b []byte) (n int, err error) {
	err = c.conn.WriteMessage(websocket.BinaryMessage, b)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

func (c *wsConn) Close() error {
	return c.conn.Close()
}

func (c *wsConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

func (c *wsConn) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

func (c *wsConn) SetDeadline(t time.Time) error {
	if err := c.conn.SetReadDeadline(t); err != nil {
		return err
	}
	return c.conn.SetWriteDeadline(t)
}

func (c *wsConn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

func (c *wsConn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}

// Client implements the hostexec.HostClient interface using the Honey REST API.
type Client struct {
	url      string
	token    string
	insecure bool
	mtls     bool
	serverCA string
	mesh     bool
	meshAddr string
	user     string
	record   hosts.Record
}

// doRequest sends a JSON POST request to the upstream Honey REST API.
func (c *Client) doRequest(ctx context.Context, path string, body any, out any) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	fullURL := strings.TrimRight(c.url, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bodyReader)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if !c.mtls && c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	tr, err := buildTransport(trustConfig{
		insecure: c.insecure,
		mtls:     c.mtls,
		serverCA: c.serverCA,
		mesh:     c.mesh,
		meshAddr: c.meshAddr,
	})
	if err != nil {
		return err
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   10 * time.Minute, // Honey proxy executions can take a while
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(errBody))
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// Run executes a command via POST /api/v1/exec on the upstream server.
func (c *Client) Run(cmd string) ([]byte, error) {
	req := map[string]any{
		"ssh_user": c.user,
		"command":  cmd,
		"records":  []hosts.Record{c.record},
	}

	var res struct {
		Results []struct {
			Output   string `json:"Output"`
			ExitCode int    `json:"ExitCode"`
			Error    string `json:"ErrMsg,omitempty"`
		} `json:"results"`
	}

	if err := c.doRequest(context.Background(), "/api/v1/exec", req, &res); err != nil {
		return nil, err
	}

	if len(res.Results) == 0 {
		return nil, fmt.Errorf("no results from upstream server")
	}

	result := res.Results[0]
	if result.Error != "" {
		return []byte(result.Output), fmt.Errorf("%s", result.Error)
	}
	if result.ExitCode != 0 {
		return []byte(result.Output), fmt.Errorf("process exited with status %d", result.ExitCode)
	}

	return []byte(result.Output), nil
}

// RunWithStreams executes a command on the upstream server over a WebSocket stream.
func (c *Client) RunWithStreams(cmd string, stdin io.Reader, stdout, _ io.Writer) error {
	wsURL := strings.Replace(c.url, "http", "ws", 1) + "/api/v1/ws/exec"
	tlsCfg, err := clientTLSConfig(c.insecure, c.mtls, c.serverCA)
	if err != nil {
		return err
	}
	token := c.token
	if c.mtls {
		token = ""
	}
	conn, err := dialWS(context.Background(), wsURL, token, tlsCfg, meshDialContext(c.mesh, c.meshAddr))
	if err != nil {
		return err
	}
	defer conn.Close()

	hello := map[string]any{"ssh_user": c.user, "record": c.record, "command": cmd}
	if err := conn.WriteJSON(hello); err != nil {
		return err
	}

	var resp map[string]any
	if err := conn.ReadJSON(&resp); err != nil {
		return err
	}
	if errStr, ok := resp["error"].(string); ok && errStr != "" {
		return fmt.Errorf("upstream exec error: %s", errStr)
	}

	errc := make(chan error, 1)
	go func() {
		if stdin == nil {
			return
		}
		buf := make([]byte, 32*1024)
		for {
			n, err := stdin.Read(buf)
			if n > 0 {
				if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	go func() {
		for {
			mt, p, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					errc <- nil
				} else {
					// Some other connection error
					errc <- err
				}
				return
			}
			if mt == websocket.TextMessage {
				// It might be a JSON error message if the server couldn't run it
				var r map[string]any
				if json.Unmarshal(p, &r) == nil {
					if e, ok := r["error"].(string); ok && e != "" {
						errc <- fmt.Errorf("remote error: %s", e)
						return
					}
				}
			}
			if stdout != nil {
				_, _ = stdout.Write(p)
			}
		}
	}()

	err = <-errc
	// On clean exit err is nil
	if err != nil && strings.Contains(err.Error(), "use of closed network connection") {
		err = nil
	}
	return err
}

// Upload streams a local file to the remote host via the upstream Honey proxy.
func (c *Client) Upload(localPath, remotePath string) error {
	f, err := safepath.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return err
	}

	recJSON, _ := json.Marshal(c.record)
	query := url.Values{
		"ssh_user": {c.user},
		"record":   {string(recJSON)},
		"path":     {remotePath},
	}
	endpoint := "/api/v1/files/remote/upload?" + query.Encode()

	var res struct {
		Success bool `json:"success"`
	}
	if err := c.doRequestWithBody(context.Background(), http.MethodPost, endpoint, f, st.Size(), &res); err != nil {
		return err
	}
	if !res.Success {
		return fmt.Errorf("upload failed")
	}
	return nil
}

// Download streams a file from the remote host to the local filesystem via the upstream Honey proxy.
func (c *Client) Download(remotePath, localPath string) error {
	f, err := safepath.Create(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	recJSON, _ := json.Marshal(c.record)
	query := url.Values{
		"ssh_user": {c.user},
		"record":   {string(recJSON)},
		"path":     {remotePath},
	}
	endpoint := "/api/v1/files/remote/download?" + query.Encode()

	return c.doDownload(context.Background(), endpoint, f)
}

func (c *Client) doRequestWithBody(ctx context.Context, method, path string, body io.Reader, size int64, out any) error {
	fullURL := strings.TrimRight(c.url, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}

	req.ContentLength = size

	req.Header.Set("Content-Type", "application/octet-stream")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	// mtls is intentionally not threaded through here (unlike doRequest) — this
	// preserves this call site's pre-existing behavior of never applying mTLS,
	// which predates the buildTransport consolidation. See transport.go.
	tr, err := buildTransport(trustConfig{
		insecure: c.insecure,
		mtls:     false,
		serverCA: c.serverCA,
		mesh:     c.mesh,
		meshAddr: c.meshAddr,
	})
	if err != nil {
		return err
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   1 * time.Hour, // large files can take time
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(errBody))
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func (c *Client) doDownload(ctx context.Context, path string, out io.Writer) error {
	fullURL := strings.TrimRight(c.url, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}

	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	// mtls is intentionally not threaded through here (unlike doRequest) — this
	// preserves this call site's pre-existing behavior of never applying mTLS,
	// which predates the buildTransport consolidation. See transport.go.
	tr, err := buildTransport(trustConfig{
		insecure: c.insecure,
		mtls:     false,
		serverCA: c.serverCA,
		mesh:     c.mesh,
		meshAddr: c.meshAddr,
	})
	if err != nil {
		return err
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   1 * time.Hour,
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(errBody))
	}

	_, err = io.Copy(out, resp.Body)
	return err
}

// ListRemoteDir requests a remote directory listing from the upstream Honey proxy.
func (c *Client) ListRemoteDir(path string) ([]hostexec.RemoteFileEntry, error) {
	req := map[string]any{
		"ssh_user": c.user,
		"record":   c.record,
		"path":     path,
	}

	var res struct {
		Path    string                     `json:"path"`
		Entries []hostexec.RemoteFileEntry `json:"entries"`
	}

	if err := c.doRequest(context.Background(), "/api/v1/files/remote/list", req, &res); err != nil {
		return nil, err
	}

	return res.Entries, nil
}

// StatRemote requests metadata for a remote file from the upstream Honey proxy.
func (c *Client) StatRemote(path string) (hostexec.RemoteFileEntry, error) {
	req := map[string]any{
		"ssh_user": c.user,
		"record":   c.record,
		"path":     path,
	}

	var res struct {
		Entry hostexec.RemoteFileEntry `json:"entry"`
	}

	if err := c.doRequest(context.Background(), "/api/v1/files/remote/stat", req, &res); err != nil {
		return hostexec.RemoteFileEntry{}, err
	}

	return res.Entry, nil
}

// MkdirAllRemote requests remote directory creation from the upstream Honey proxy.
func (c *Client) MkdirAllRemote(path string) error {
	req := map[string]any{
		"ssh_user": c.user,
		"record":   c.record,
		"path":     path,
	}

	var res struct {
		Success bool `json:"success"`
	}

	if err := c.doRequest(context.Background(), "/api/v1/files/remote/mkdir", req, &res); err != nil {
		return err
	}
	if !res.Success {
		return fmt.Errorf("mkdir failed on upstream proxy")
	}

	return nil
}

// RemoveRemote requests a remote file or directory deletion from the upstream Honey proxy.
func (c *Client) RemoveRemote(path string, recursive bool) error {
	req := map[string]any{
		"ssh_user":  c.user,
		"record":    c.record,
		"path":      path,
		"recursive": recursive,
	}

	var res struct {
		Success bool `json:"success"`
	}

	if err := c.doRequest(context.Background(), "/api/v1/files/remote/remove", req, &res); err != nil {
		return err
	}
	if !res.Success {
		return fmt.Errorf("remove failed on upstream proxy")
	}

	return nil
}

// StartRemoteForward is not supported for upstream Honey proxying yet.
func (c *Client) StartRemoteForward(_ context.Context, _ string, _ int, _ string, _ int) (string, func(), error) {
	return "", nil, fmt.Errorf("StartRemoteForward is not supported for upstream Honey proxying yet")
}

// StartDynamicForward is not supported for upstream Honey proxying yet.
func (c *Client) StartDynamicForward(_ context.Context, _ string, _ int) (string, int, func(), error) {
	return "", 0, nil, fmt.Errorf("StartDynamicForward is not supported for upstream Honey proxying yet")
}

// StartUDPRelay is not supported for upstream Honey proxying yet.
func (c *Client) StartUDPRelay(_ context.Context, _ string, _ int, _ string, _ int, _ bool) (string, int, func(), error) {
	return "", 0, nil, fmt.Errorf("StartUDPRelay is not supported for upstream Honey proxying yet")
}

// StartTunForward is not supported for upstream Honey proxying yet.
func (c *Client) StartTunForward(_ context.Context, _ string, _ string, _ int, _, _ int) (string, func(), error) {
	return "", nil, fmt.Errorf("StartTunForward is not supported for upstream Honey proxying yet")
}

// Close closes the proxy client.
func (c *Client) Close() error {
	return nil
}
