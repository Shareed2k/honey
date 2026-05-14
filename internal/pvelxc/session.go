package pvelxc

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/shareed2k/honey/internal/hostexec"
)

// APIBase normalizes a Proxmox API root to include /api2/json.
func APIBase(raw string) string {
	u := strings.TrimRight(strings.TrimSpace(raw), "/")
	if strings.HasSuffix(u, "/api2/json") {
		return u
	}
	return u + "/api2/json"
}

type termProxyData struct {
	User   string
	Ticket string
	Port   string
}

func guestPathSegment(kind string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "lxc":
		return "lxc", nil
	case "qemu":
		return "qemu", nil
	default:
		return "", fmt.Errorf("proxmox guest kind: want lxc or qemu, got %q", kind)
	}
}

func postTermProxy(ctx context.Context, b hostexec.ProxmoxBackendRuntime, guest string, node string, vmid int) (termProxyData, error) {
	var zero termProxyData
	seg, err := guestPathSegment(guest)
	if err != nil {
		return zero, err
	}
	apiBase := APIBase(b.URL)
	termURL := fmt.Sprintf("%s/nodes/%s/%s/%d/termproxy", apiBase, url.PathEscape(node), seg, vmid)
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: b.Insecure}} // #nosec G402
	client := &http.Client{Transport: tr, Timeout: 60 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, termURL, nil)
	if err != nil {
		return zero, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("PVEAPIToken=%s=%s", strings.TrimSpace(b.TokenID), strings.TrimSpace(b.TokenSec)))
	resp, err := client.Do(req)
	if err != nil {
		return zero, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return zero, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(body))
		err := fmt.Errorf("proxmox termproxy: HTTP %d: %s", resp.StatusCode, msg)
		if seg == "qemu" && strings.Contains(strings.ToLower(msg), "serial") {
			return zero, fmt.Errorf("%w (QEMU: add a serial device under VM → Hardware → Add → Serial Port, or use the web VNC action for a graphical console)", err)
		}
		return zero, err
	}
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return zero, fmt.Errorf("proxmox termproxy: parse: %w", err)
	}
	if envelope.Data == nil {
		return zero, fmt.Errorf("proxmox termproxy: missing data")
	}
	user, _ := envelope.Data["user"].(string)
	ticket, _ := envelope.Data["ticket"].(string)
	port := strings.TrimSpace(fmt.Sprint(envelope.Data["port"]))
	if ticket == "" || port == "" || user == "" {
		return zero, fmt.Errorf("proxmox termproxy: incomplete response")
	}
	return termProxyData{User: user, Ticket: ticket, Port: port}, nil
}

// SerialWebSocketURL builds the wss URL for LXC or QEMU serial vncwebsocket (vncticket is encoded via url.Values).
func SerialWebSocketURL(apiBase, node, guest string, vmid int, port, vncticket string) (string, error) {
	seg, err := guestPathSegment(guest)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(apiBase)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid Proxmox API URL")
	}
	scheme := "ws"
	if u.Scheme == "https" {
		scheme = "wss"
	}
	q := url.Values{}
	q.Set("port", port)
	q.Set("vncticket", vncticket)
	path := fmt.Sprintf("/api2/json/nodes/%s/%s/%d/vncwebsocket", url.PathEscape(node), seg, vmid)
	out := url.URL{Scheme: scheme, Host: u.Host, Path: path, RawQuery: q.Encode()}
	return out.String(), nil
}

// Session is an authenticated Proxmox LXC vncwebsocket after the PVE xterm login handshake and initial resize.
type Session struct {
	mu sync.Mutex
	c  *websocket.Conn
}

func (s *Session) writeMessage(messageType int, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.c.WriteMessage(messageType, data)
}

// WriteRawTTYInput sends one PVE stdin frame (0:len:data).
func (s *Session) WriteRawTTYInput(p []byte) error {
	if len(p) == 0 {
		return nil
	}
	prefix := []byte(fmt.Sprintf("0:%d:", len(p)))
	return s.writeMessage(websocket.BinaryMessage, append(prefix, p...))
}

// WriteResize sends PVE resize frame (rows, cols) per PVE/xterm.js convention.
func (s *Session) WriteResize(rows, cols int) error {
	if rows <= 0 || cols <= 0 {
		return nil
	}
	return s.writeMessage(websocket.BinaryMessage, []byte(fmt.Sprintf("1:%d:%d:", rows, cols)))
}

// WritePing sends the PVE keepalive frame.
func (s *Session) WritePing() error {
	return s.writeMessage(websocket.BinaryMessage, []byte("2"))
}

// ReadMessage reads the next websocket message from PVE (single-reader goroutine).
func (s *Session) ReadMessage() (messageType int, p []byte, err error) {
	return s.c.ReadMessage()
}

// Close closes the PVE websocket.
func (s *Session) Close() error {
	return s.c.Close()
}

// OpenSession performs termproxy for LXC or QEMU serial, connects vncwebsocket, login handshake, and initial resize.
// guest must be "lxc" or "qemu" (same as record meta kind).
func OpenSession(ctx context.Context, b hostexec.ProxmoxBackendRuntime, guest, node string, vmid int, rows, cols int) (*Session, error) {
	if rows <= 0 {
		rows = 32
	}
	if cols <= 0 {
		cols = 120
	}
	tp, err := postTermProxy(ctx, b, guest, node, vmid)
	if err != nil {
		return nil, err
	}
	wsURL, err := SerialWebSocketURL(APIBase(b.URL), node, guest, vmid, tp.Port, tp.Ticket)
	if err != nil {
		return nil, err
	}
	d := websocket.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: b.Insecure}, // #nosec G402
	}
	hdr := http.Header{}
	hdr.Set("Authorization", fmt.Sprintf("PVEAPIToken=%s=%s", strings.TrimSpace(b.TokenID), strings.TrimSpace(b.TokenSec)))
	c, _, err := d.DialContext(ctx, wsURL, hdr)
	if err != nil {
		return nil, err
	}
	sess := &Session{c: c}
	login := []byte(tp.User + ":" + tp.Ticket + "\n")
	if err := sess.writeMessage(websocket.BinaryMessage, login); err != nil {
		_ = c.Close()
		return nil, err
	}
	_, ack, err := c.ReadMessage()
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	if string(ack) != "OK" {
		_ = c.Close()
		return nil, fmt.Errorf("proxmox vncwebsocket: expected OK, got %q", string(ack))
	}
	if err := sess.WriteResize(rows, cols); err != nil {
		_ = c.Close()
		return nil, err
	}
	return sess, nil
}
