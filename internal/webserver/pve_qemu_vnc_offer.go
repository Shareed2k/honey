package webserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/proxmoxprovider"
	"github.com/shareed2k/honey/internal/pvelxc"
)

// PveQemuVncOfferRequest is the JSON body for POST /api/v1/pve-qemu-vnc-offer.
type PveQemuVncOfferRequest struct {
	Record hosts.Record `json:"record"`
}

// PveQemuVncOfferResponse is returned on success.
type PveQemuVncOfferResponse struct {
	SessionID   string `json:"session_id"`
	VNCPassword string `json:"vnc_password"`
}

const pveQemuVncOfferTTL = 90 * time.Second

// pveQemuVncOfferSession holds one vncproxy result for a single WebSocket dial (ticket is short-lived on PVE too).
type pveQemuVncOfferSession struct {
	BackendName string
	Node        string
	VMID        int
	Port        string
	Ticket      string
	Expires     time.Time
}

func randomSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// handlePveQemuVncOffer requests a short-lived Proxmox QEMU VNC ticket for noVNC.
// @Summary Proxmox QEMU VNC offer
// @Tags proxmox
// @Accept json
// @Produce json
// @Param body body PveQemuVncOfferRequest true "record for a QEMU VM on Proxmox"
// @Success 200 {object} PveQemuVncOfferResponse
// @Failure 400 {object} map[string]string
// @Router /api/v1/pve-qemu-vnc-offer [post]
// @Security BearerAuth
func (s *Server) handlePveQemuVncOffer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, `{"error":"read body"}`, http.StatusBadRequest)
		return
	}
	var req PveQemuVncOfferRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	rec := req.Record
	if !pvelxc.ShouldUsePVEQemuWebVNC(rec) {
		http.Error(w, `{"error":"qemu vnc is not available for this host"}`, http.StatusBadRequest)
		return
	}
	b, ok := proxmoxprovider.BackendByName(rec.Meta["backend_name"])
	if !ok {
		http.Error(w, `{"error":"proxmox backend not configured"}`, http.StatusBadRequest)
		return
	}
	node := strings.TrimSpace(rec.Meta["node"])
	vmid, err := strconv.Atoi(strings.TrimSpace(rec.Meta["vmid"]))
	if err != nil || vmid <= 0 || node == "" {
		http.Error(w, `{"error":"proxmox record missing node or vmid"}`, http.StatusBadRequest)
		return
	}

	vp, err := pvelxc.PostQemuVncProxy(r.Context(), b, node, vmid)
	if err != nil {
		http.Error(w, `{"error":"`+escapeJSON(err.Error())+`"}`, http.StatusBadGateway)
		return
	}

	id, err := randomSessionID()
	if err != nil {
		http.Error(w, `{"error":"session id"}`, http.StatusInternalServerError)
		return
	}
	sess := pveQemuVncOfferSession{
		BackendName: strings.TrimSpace(rec.Meta["backend_name"]),
		Node:        node,
		VMID:        vmid,
		Port:        vp.Port,
		Ticket:      vp.Ticket,
		Expires:     time.Now().Add(pveQemuVncOfferTTL),
	}

	s.pveQemuVncMu.Lock()
	if s.pveQemuVncByID == nil {
		s.pveQemuVncByID = make(map[string]pveQemuVncOfferSession)
	}
	s.pveQemuVncByID[id] = sess
	s.pveQemuVncMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(PveQemuVncOfferResponse{
		SessionID:   id,
		VNCPassword: vp.Ticket,
	})
}

// handleWebProxmoxQemuVNC upgrades to WebSocket and raw-bridges to PVE QEMU graphics vncwebsocket (RFB).
// Query: token (honey), vnc_session (one-time id from POST /api/v1/pve-qemu-vnc-offer).
func (s *Server) handleWebProxmoxQemuVNC(w http.ResponseWriter, r *http.Request) {
	if !tokenFromRequest(r, s.opts.Token) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	sid := strings.TrimSpace(r.URL.Query().Get("vnc_session"))
	if sid == "" {
		http.Error(w, `{"error":"missing vnc_session (use POST /api/v1/pve-qemu-vnc-offer first)"}`, http.StatusBadRequest)
		return
	}

	s.pveQemuVncMu.Lock()
	if s.pveQemuVncByID == nil {
		s.pveQemuVncMu.Unlock()
		http.Error(w, `{"error":"unknown or expired vnc_session"}`, http.StatusBadRequest)
		return
	}
	sess, ok := s.pveQemuVncByID[sid]
	if !ok {
		s.pveQemuVncMu.Unlock()
		http.Error(w, `{"error":"unknown or expired vnc_session"}`, http.StatusBadRequest)
		return
	}
	if time.Now().After(sess.Expires) {
		delete(s.pveQemuVncByID, sid)
		s.pveQemuVncMu.Unlock()
		http.Error(w, `{"error":"unknown or expired vnc_session"}`, http.StatusBadRequest)
		return
	}
	delete(s.pveQemuVncByID, sid)
	s.pveQemuVncMu.Unlock()

	b, ok := proxmoxprovider.BackendByName(sess.BackendName)
	if !ok {
		http.Error(w, `{"error":"proxmox backend not configured"}`, http.StatusBadRequest)
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	defer s.trackWSConnection("pve_vnc")()

	pveConn, err := pvelxc.DialQemuGraphicsVNCWS(context.Background(), b, sess.Node, sess.VMID, sess.Port, sess.Ticket)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
		return
	}
	defer func() { _ = pveConn.Close() }()

	pvelxc.BridgeVNCWebSockets(conn, pveConn)
}
