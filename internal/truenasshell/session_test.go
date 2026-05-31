package truenasshell

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/truenasprovider"
)

func TestReadShellConnected(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		c, _ := up.Upgrade(w, r, nil)
		_ = c.WriteJSON(map[string]string{"msg": "connected", "id": "sess-1"})
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	id, err := readShellConnected(c)
	if err != nil {
		t.Fatal(err)
	}
	if id != "sess-1" {
		t.Fatalf("id=%q", id)
	}
}

func TestOpenSession_applianceMock(t *testing.T) {
	var mu sync.Mutex
	tokenIssued := false
	shellAuthed := false

	mux := http.NewServeMux()
	mux.HandleFunc("/api/current", func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		for {
			_, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				Method string `json:"method"`
				ID     string `json:"id"`
			}
			if json.Unmarshal(data, &req) != nil {
				continue
			}
			mu.Lock()
			switch req.Method {
			case "auth.login_ex":
				_ = c.WriteJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]string{"response_type": "SUCCESS"},
				})
			case "auth.generate_token":
				tokenIssued = true
				_ = c.WriteJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  "tok-abc",
				})
			case "core.resize_shell":
				_ = c.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": nil})
			}
			mu.Unlock()
		}
	})
	mux.HandleFunc("/websocket/shell", func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		_, data, err := c.ReadMessage()
		if err != nil {
			return
		}
		var auth shellAuthMsg
		if json.Unmarshal(data, &auth) != nil || auth.Token != "tok-abc" {
			_ = c.WriteJSON(map[string]string{"msg": "failed"})
			return
		}
		shellAuthed = true
		_ = c.WriteJSON(map[string]string{"msg": "connected", "id": "sh-1"})
		_ = c.WriteMessage(websocket.BinaryMessage, []byte("ok"))
	})

	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "https://")
	b := truenasprovider.TrueNASBackendRuntime{
		Name:     "lab",
		URL:      "https://" + host,
		Username: "root",
		APIKey:   "1-key",
		Insecure: true,
	}
	rec := hosts.Record{
		Provider: "truenas",
		Meta:     map[string]string{"kind": "appliance"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sess, err := OpenSession(ctx, b, rec, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	mu.Lock()
	okTok, okShell := tokenIssued, shellAuthed
	mu.Unlock()
	if !okTok || !okShell {
		t.Fatalf("token=%v shell=%v", okTok, okShell)
	}

	mt, data, err := sess.ReadMessage()
	if err != nil || mt != websocket.BinaryMessage || string(data) != "ok" {
		t.Fatalf("read mt=%d data=%q err=%v", mt, data, err)
	}
}

func TestOpenSession_virtInstanceMock(t *testing.T) {
	var gotVirtID string

	mux := http.NewServeMux()
	mux.HandleFunc("/api/current", func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		for {
			_, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				Method string `json:"method"`
				ID     string `json:"id"`
			}
			if json.Unmarshal(data, &req) != nil {
				continue
			}
			switch req.Method {
			case "auth.login_ex":
				_ = c.WriteJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]string{"response_type": "SUCCESS"},
				})
			case "auth.generate_token":
				_ = c.WriteJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  "tok-abc",
				})
			case "core.resize_shell":
				_ = c.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": nil})
			}
		}
	})
	mux.HandleFunc("/websocket/shell", func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		_, data, err := c.ReadMessage()
		if err != nil {
			return
		}
		var auth shellAuthMsg
		if json.Unmarshal(data, &auth) != nil || auth.Token != "tok-abc" {
			_ = c.WriteJSON(map[string]string{"msg": "failed"})
			return
		}
		if auth.Options != nil {
			if v, ok := auth.Options["virt_instance_id"].(string); ok {
				gotVirtID = v
			}
		}
		_ = c.WriteJSON(map[string]string{"msg": "connected", "id": "sh-virt"})
	})

	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "https://")
	b := truenasprovider.TrueNASBackendRuntime{
		Name:     "lab",
		URL:      "https://" + host,
		Username: "root",
		APIKey:   "1-key",
		Insecure: true,
	}
	rec := hosts.Record{
		Provider: "truenas",
		Name:     "web",
		Meta: map[string]string{
			"kind": "virt_instance",
			"id":   "incus-web-1",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sess, err := OpenSession(ctx, b, rec, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	if gotVirtID != "incus-web-1" {
		t.Fatalf("virt_instance_id=%q want incus-web-1", gotVirtID)
	}
}
