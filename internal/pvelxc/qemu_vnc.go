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
	"time"

	"github.com/gorilla/websocket"

	"github.com/shareed2k/honey/internal/hostexec"
)

// QemuVncProxyData is the vncproxy response for QEMU graphics console.
type QemuVncProxyData struct {
	Port   string
	Ticket string
}

// PostQemuVncProxy calls POST /nodes/{node}/qemu/{vmid}/vncproxy with websocket=1.
func PostQemuVncProxy(ctx context.Context, b hostexec.ProxmoxBackendRuntime, node string, vmid int) (QemuVncProxyData, error) {
	var zero QemuVncProxyData
	apiBase := APIBase(b.URL)
	vncURL := fmt.Sprintf("%s/nodes/%s/qemu/%d/vncproxy", apiBase, url.PathEscape(node), vmid)
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: b.Insecure}} // #nosec G402
	client := &http.Client{Transport: tr, Timeout: 60 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, vncURL, strings.NewReader("websocket=1"))
	if err != nil {
		return zero, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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
		return zero, fmt.Errorf("proxmox qemu vncproxy: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return zero, fmt.Errorf("proxmox qemu vncproxy: parse: %w", err)
	}
	if envelope.Data == nil {
		return zero, fmt.Errorf("proxmox qemu vncproxy: missing data")
	}
	ticket, _ := envelope.Data["ticket"].(string)
	port := strings.TrimSpace(fmt.Sprint(envelope.Data["port"]))
	if ticket == "" || port == "" {
		return zero, fmt.Errorf("proxmox qemu vncproxy: incomplete response")
	}
	return QemuVncProxyData{Port: port, Ticket: ticket}, nil
}

// QemuGraphicsWebSocketURL builds the wss URL for QEMU graphics vncwebsocket (RFB over WebSocket).
func QemuGraphicsWebSocketURL(apiBase, node string, vmid int, port, vncticket string) (string, error) {
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
	path := fmt.Sprintf("/api2/json/nodes/%s/qemu/%d/vncwebsocket", url.PathEscape(node), vmid)
	out := url.URL{Scheme: scheme, Host: u.Host, Path: path, RawQuery: q.Encode()}
	return out.String(), nil
}

// DialQemuGraphicsVNCWS dials PVE qemu vncwebsocket using an existing vncproxy port and ticket (single-use; short-lived).
func DialQemuGraphicsVNCWS(ctx context.Context, b hostexec.ProxmoxBackendRuntime, node string, vmid int, port, ticket string) (*websocket.Conn, error) {
	wsURL, err := QemuGraphicsWebSocketURL(APIBase(b.URL), node, vmid, port, ticket)
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
	return c, nil
}

// DialQemuGraphicsVNC opens a websocket to PVE for the VM graphical console (no xterm login; RFB starts from server).
func DialQemuGraphicsVNC(ctx context.Context, b hostexec.ProxmoxBackendRuntime, node string, vmid int) (*websocket.Conn, error) {
	vp, err := PostQemuVncProxy(ctx, b, node, vmid)
	if err != nil {
		return nil, err
	}
	return DialQemuGraphicsVNCWS(ctx, b, node, vmid, vp.Port, vp.Ticket)
}
