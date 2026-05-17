package ui

import (
	"sync"

	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/sshclient"
)

var (
	dockerBorrowMu    sync.RWMutex
	dockerBorrowCache *ClientCache
)

// SetDockerSSHBorrowCache sets the TUI client cache used to reuse SSH for honey-ssh Docker transport.
func SetDockerSSHBorrowCache(c *ClientCache) {
	dockerBorrowMu.Lock()
	dockerBorrowCache = c
	dockerBorrowMu.Unlock()
}

func init() {
	hostexec.RegisterDockerSSHBorrower(borrowDockerSSHFromCache)
}

func borrowDockerSSHFromCache(user string, hop hosts.Record) (*ssh.Client, bool) {
	dockerBorrowMu.RLock()
	c := dockerBorrowCache
	dockerBorrowMu.RUnlock()
	if c == nil {
		return nil, false
	}
	hc, err := c.GetOrDial(user, hop)
	if err != nil {
		return nil, false
	}
	if h, ok := hc.(*sshclient.HoneyClient); ok {
		if leaf := h.LeafSSH(); leaf != nil {
			return leaf, true
		}
	}
	return nil, false
}
