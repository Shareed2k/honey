package truenasshell

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/truenasprovider"
)

const (
	defaultDialTimeout = 30 * time.Second
	tokenTTL           = 600
)

type shellAuthMsg struct {
	Token   string         `json:"token"`
	Options map[string]any `json:"options,omitempty"`
}

type shellConnectResp struct {
	Msg   string `json:"msg"`
	ID    string `json:"id"`
	Error *struct {
		Reason string `json:"reason"`
	} `json:"error"`
}

// Session bridges a TrueNAS /websocket/shell PTY; resize uses the JSON-RPC API connection.
type Session struct {
	mu      sync.Mutex
	ctx     context.Context
	api     *truenasprovider.Client
	shell   *websocket.Conn
	shellID string
}

func (s *Session) writeMessage(messageType int, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shell == nil {
		return fmt.Errorf("truenas shell connection closed")
	}
	return s.shell.WriteMessage(messageType, data)
}

// WriteBinary sends stdin bytes to the TrueNAS shell websocket.
func (s *Session) WriteBinary(p []byte) error {
	if len(p) == 0 {
		return nil
	}
	return s.writeMessage(websocket.BinaryMessage, p)
}

// Resize calls core.resize_shell on the API websocket.
func (s *Session) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	if s.shellID == "" {
		return nil
	}
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s.api.Call(ctx, "core.resize_shell", []any{s.shellID, cols, rows}, nil)
}

// ReadMessage reads the next message from the shell websocket.
func (s *Session) ReadMessage() (messageType int, p []byte, err error) {
	s.mu.Lock()
	conn := s.shell
	s.mu.Unlock()
	if conn == nil {
		return 0, nil, fmt.Errorf("truenas shell connection closed")
	}
	return conn.ReadMessage()
}

// SetReadDeadline sets the read deadline on the shell websocket.
func (s *Session) SetReadDeadline(t time.Time) error {
	s.mu.Lock()
	conn := s.shell
	s.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("truenas shell connection closed")
	}
	return conn.SetReadDeadline(t)
}

// Close closes shell and API connections. Safe to call more than once.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var err error
	if s.shell != nil {
		err = s.shell.Close()
		s.shell = nil
	}
	if s.api != nil {
		if cerr := s.api.Close(); err == nil {
			err = cerr
		}
		s.api = nil
	}
	return err
}

// OpenSession authenticates to TrueNAS, opens /websocket/shell for rec, and applies initial resize.
func OpenSession(ctx context.Context, b truenasprovider.TrueNASBackendRuntime, rec hosts.Record, rows, cols int) (*Session, error) {
	if rows <= 0 {
		rows = 32
	}
	if cols <= 0 {
		cols = 120
	}
	apiWS, err := APIWSURL(b.URL, b.Insecure)
	if err != nil {
		return nil, err
	}
	shellWS, err := ShellWSURL(apiWS)
	if err != nil {
		return nil, err
	}

	user := strings.TrimSpace(b.Username)
	if user == "" {
		user = "root"
	}

	dialCtx, cancel := context.WithTimeout(ctx, defaultDialTimeout)
	defer cancel()

	api, err := truenasprovider.NewClient(dialCtx, apiWS, user, b.APIKey, b.Insecure)
	if err != nil {
		return nil, err
	}

	opts, err := resolveShellOptions(dialCtx, api, rec)
	if err != nil {
		_ = api.Close()
		return nil, err
	}

	var token string
	if err := api.Call(dialCtx, "auth.generate_token", []any{tokenTTL, map[string]any{}, false}, &token); err != nil {
		_ = api.Close()
		return nil, fmt.Errorf("truenas generate_token: %w", err)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		_ = api.Close()
		return nil, fmt.Errorf("truenas generate_token: empty token")
	}

	shellConn, _, err := truenasprovider.Dialer(b.Insecure).DialContext(dialCtx, shellWS, nil)
	if err != nil {
		_ = api.Close()
		return nil, fmt.Errorf("truenas shell dial: %w", err)
	}

	auth := shellAuthMsg{Token: token}
	if len(opts) > 0 {
		auth.Options = opts
	}
	if err := shellConn.WriteJSON(auth); err != nil {
		_ = shellConn.Close()
		_ = api.Close()
		return nil, fmt.Errorf("truenas shell auth write: %w", err)
	}

	shellID, err := readShellConnected(shellConn)
	if err != nil {
		_ = shellConn.Close()
		_ = api.Close()
		return nil, err
	}

	sess := &Session{ctx: ctx, api: api, shell: shellConn, shellID: shellID}
	if err := sess.Resize(cols, rows); err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("truenas shell resize: %w", err)
	}
	return sess, nil
}

func readShellConnected(conn *websocket.Conn) (string, error) {
	deadline := time.Now().Add(defaultDialTimeout)
	for time.Now().Before(deadline) {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			return "", fmt.Errorf("truenas shell handshake read: %w", err)
		}
		if mt != websocket.TextMessage {
			continue
		}
		var resp shellConnectResp
		if err := json.Unmarshal(data, &resp); err != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(resp.Msg)) {
		case "connected":
			if strings.TrimSpace(resp.ID) == "" {
				return "", fmt.Errorf("truenas shell: connected without session id")
			}
			return resp.ID, nil
		case "failed":
			reason := "shell authentication failed"
			if resp.Error != nil && strings.TrimSpace(resp.Error.Reason) != "" {
				reason = strings.TrimSpace(resp.Error.Reason)
			}
			return "", fmt.Errorf("truenas shell: %s", reason)
		}
	}
	return "", fmt.Errorf("truenas shell: timed out waiting for connected")
}
