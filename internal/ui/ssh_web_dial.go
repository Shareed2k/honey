package ui

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/sshclient"
)

// DialSSHLeafForRecord returns a leaf *ssh.Client for SSH to hosts.Record.PrimaryIP (same transport as the TUI).
// Kubernetes pod records are not supported for raw SSH in this helper.
func DialSSHLeafForRecord(user string, r hosts.Record) (*ssh.Client, func(), error) {
	if r.Provider == "k8s" && r.Meta["kind"] == "pod" {
		return nil, nil, fmt.Errorf("web SSH for Kubernetes pods is not supported in this version")
	}
	host := strings.TrimSpace(r.PrimaryIP)
	if host == "" {
		return nil, nil, fmt.Errorf("no IP for selected host")
	}
	sshPort := 0
	if p, ok := hosts.MetaSSHPort(&r); ok {
		sshPort = p
	}
	return sshclient.DialSSHClient(user, host, sshPort)
}
