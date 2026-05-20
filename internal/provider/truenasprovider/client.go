// Package truenasprovider discovers TrueNAS SCALE hosts via the WebSocket JSON-RPC API.
package truenasprovider

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/gorilla/websocket"
)

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
	ID      string `json:"id"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
	ID      string          `json:"id"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    *struct {
		Reason string `json:"reason"`
	} `json:"data,omitempty"`
}

func (e *jsonRPCError) Error() string {
	if e == nil {
		return "json-rpc error"
	}
	if e.Data != nil && e.Data.Reason != "" {
		return e.Data.Reason
	}
	return e.Message
}

// Client is a minimal TrueNAS SCALE 25.04+ JSON-RPC over WebSocket client.
type Client struct {
	wsURL    string
	username string
	apiKey   string
	insecure bool

	conn   *websocket.Conn
	nextID atomic.Int64
}

// Dialer returns a WebSocket dialer with optional TLS skip-verify for self-signed certs.
func Dialer(insecure bool) *websocket.Dialer {
	d := &websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: defaultDialTimeout,
	}
	if insecure {
		// #nosec G402 -- user-configured for self-signed TrueNAS certs
		d.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return d
}

// NewClient dials and authenticates against a TrueNAS controller.
func NewClient(ctx context.Context, wsURL, username, apiKey string, insecure bool) (*Client, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		username = "root"
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("truenas api_key is required")
	}

	conn, _, err := Dialer(insecure).DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("truenas websocket dial: %w", err)
	}

	c := &Client{
		wsURL:    wsURL,
		username: username,
		apiKey:   apiKey,
		insecure: insecure,
		conn:     conn,
	}
	if err := c.authenticate(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return c, nil
}

// Close closes the WebSocket connection.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

// Call invokes a TrueNAS API method and unmarshals the result into out (when non-nil).
func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("truenas client is closed")
	}
	id := fmt.Sprintf("req-%d", c.nextID.Add(1))
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  wrapParams(params),
		ID:      id,
	}
	if err := c.conn.WriteJSON(req); err != nil {
		return fmt.Errorf("truenas %s write: %w", method, err)
	}

	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("truenas %s: %w", method, err)
		}
		var resp jsonRPCResponse
		if err := c.conn.ReadJSON(&resp); err != nil {
			return fmt.Errorf("truenas %s read: %w", method, err)
		}
		if resp.ID != id {
			// Ignore job/collection notifications.
			continue
		}
		if resp.Error != nil {
			return fmt.Errorf("truenas %s: %w", method, resp.Error)
		}
		if out == nil {
			return nil
		}
		if len(resp.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("truenas %s decode: %w", method, err)
		}
		return nil
	}
}

func (c *Client) authenticate(ctx context.Context) error {
	var result struct {
		ResponseType string `json:"response_type"`
	}
	err := c.Call(ctx, "auth.login_ex", []any{map[string]string{
		"mechanism": "API_KEY_PLAIN",
		"username":  c.username,
		"api_key":   c.apiKey,
	}}, &result)
	if err != nil {
		return fmt.Errorf("truenas auth: %w", err)
	}
	if err := authResponseError(result.ResponseType, c.wsURL); err != nil {
		return fmt.Errorf("truenas auth: %w", err)
	}
	return nil
}

// authResponseError returns nil for SUCCESS; otherwise an actionable auth failure.
func authResponseError(responseType, wsURL string) error {
	switch strings.ToUpper(strings.TrimSpace(responseType)) {
	case "SUCCESS":
		return nil
	case "AUTH_ERR":
		msg := "invalid API key or username (use the full key including id prefix, e.g. 1-…, and the user that owns the key)"
		if strings.HasPrefix(strings.ToLower(wsURL), "ws://") {
			msg += "; use https:// in the backend URL — API keys require TLS (insecure skips cert verify only; HTTP may revoke keys)"
		}
		return fmt.Errorf("%s", msg)
	case "EXPIRED":
		return fmt.Errorf("API key or credential expired; create a new key in TrueNAS (Credentials → API Keys)")
	case "OTP_REQUIRED":
		return fmt.Errorf("two-factor authentication required; use a non-2FA user or API key (OTP flow is not supported in honey v1)")
	case "REDIRECT":
		return fmt.Errorf("redirect-based authentication is not supported")
	default:
		if strings.TrimSpace(responseType) == "" {
			return fmt.Errorf("empty response_type from auth.login_ex")
		}
		return fmt.Errorf("unexpected response_type %q", responseType)
	}
}

func wrapParams(params any) any {
	if params == nil {
		return nil
	}
	if _, ok := params.([]any); ok {
		return params
	}
	return []any{params}
}
