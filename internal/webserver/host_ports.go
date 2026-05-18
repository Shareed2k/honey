package webserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/shareed2k/honey/internal/hosts"
)

// HostPortsRequest is the JSON body for POST /api/v1/host-ports.
type HostPortsRequest struct {
	SSHUser string       `json:"ssh_user"`
	Record  hosts.Record `json:"record"`
}

// HostPortsResponse is returned by POST /api/v1/host-ports.
type HostPortsResponse struct {
	Ports []string `json:"ports"`
}

// handleHostPorts discovers listening TCP ports on a host over SSH.
// @Summary List listening ports on a host
// @Tags search
// @Accept json
// @Produce json
// @Param body body HostPortsRequest true "ssh_user and host record"
// @Success 200 {object} HostPortsResponse
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/v1/host-ports [post]
// @Security BearerAuth
func (s *Server) handleHostPorts(w http.ResponseWriter, r *http.Request) {
	var req HostPortsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, fmt.Errorf("invalid json"), http.StatusBadRequest)
		return
	}

user := strings.TrimSpace(req.SSHUser)
	if user == "" {
		if cfg := s.opts.Config; cfg != nil && cfg.Defaults.SSHUser != "" {
			user = cfg.Defaults.SSHUser
		}
	}
	if user == "" {
		user = os.Getenv("USER")
	}

	client, err := s.fileClientCache.GetOrDial(user, req.Record)
	if err != nil || client == nil {
		httpError(w, fmt.Errorf("failed to connect to host: %v", err), http.StatusInternalServerError)
		return
	}

	cmd := `if command -v ss >/dev/null 2>&1; then ss -tlnH | awk '{print $4}' | awk -F: '{print $NF}' | sort -nu; else netstat -tln | awk '/LISTEN/ {print $4}' | awk -F: '{print $NF}' | sort -nu; fi`
	out, err := client.Run(cmd)
	if err != nil {
		httpError(w, fmt.Errorf("failed to run port discovery: %v", err), http.StatusInternalServerError)
		return
	}

	var ports []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			if _, err := strconv.Atoi(line); err == nil {
				ports = append(ports, line)
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(HostPortsResponse{Ports: ports})
}
