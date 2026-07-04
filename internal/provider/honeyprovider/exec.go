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
	"os"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
)

// Executor implements the hostexec.Executor interface to proxy connections through an upstream Honey server.
type Executor struct {
	URL      string
	Token    string
	Insecure bool
}

// Dial creates a new HostClient that proxies execution to the upstream Honey server.
func (e *Executor) Dial(user string, r hosts.Record) (hostexec.HostClient, error) {
	return &Client{
		url:      e.URL,
		token:    e.Token,
		insecure: e.Insecure,
		user:     user,
		record:   r,
	}, nil
}

// RunInteractive is not currently implemented for proxy proxying.
func (e *Executor) RunInteractive(_ string, _ hosts.Record) error {
	return fmt.Errorf("interactive proxying via honey upstream is not yet implemented")
}

// RunTunnel is not currently implemented for proxy proxying.
func (e *Executor) RunTunnel(_ context.Context, _ string, _ hosts.Record, _ string, _ io.Writer) error {
	return fmt.Errorf("tunnel proxying via honey upstream is not yet implemented")
}

// DialUpstream is not currently implemented for proxy proxying.
func (e *Executor) DialUpstream(_ context.Context, _ string, _ hosts.Record, _ string) (net.Conn, error) {
	return nil, fmt.Errorf("upstream dial proxying via honey upstream is not yet implemented")
}

// Client implements the hostexec.HostClient interface using the Honey REST API.
type Client struct {
	url      string
	token    string
	insecure bool
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
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	tr := &http.Transport{
		Proxy:           http.ProxyFromEnvironment,
		TLSClientConfig: &tls.Config{InsecureSkipVerify: c.insecure}, // #nosec G402
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

// RunWithStreams is not supported for upstream Honey proxying.
func (c *Client) RunWithStreams(_ string, _ io.Reader, _, _ io.Writer) error {
	return fmt.Errorf("RunWithStreams is not supported for upstream Honey proxying")
}

// Upload streams a local file to the remote host via the upstream Honey proxy.
func (c *Client) Upload(localPath, remotePath string) error {
	// #nosec G304 -- localPath originates from caller context, often inside the sandbox
	f, err := os.Open(localPath)
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
	// #nosec G304 -- localPath originates from caller context, often inside the sandbox
	f, err := os.Create(localPath)
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

	tr := &http.Transport{
		Proxy:           http.ProxyFromEnvironment,
		TLSClientConfig: &tls.Config{InsecureSkipVerify: c.insecure}, // #nosec G402
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

	tr := &http.Transport{
		Proxy:           http.ProxyFromEnvironment,
		TLSClientConfig: &tls.Config{InsecureSkipVerify: c.insecure}, // #nosec G402
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

// StartLocalForward is not supported for upstream Honey proxying yet.
func (c *Client) StartLocalForward(_ context.Context, _ string, _ int, _ string, _ int) (string, int, func(), error) {
	return "", 0, nil, fmt.Errorf("StartLocalForward is not supported for upstream Honey proxying yet")
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
