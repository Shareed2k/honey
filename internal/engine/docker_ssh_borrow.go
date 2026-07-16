package engine

import (
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/sshclient"
)

// BorrowSSH is used to wire up the ExecRegistry SSHBorrower.
func (c *ClientCache) BorrowSSH(user string, hop hosts.Record) (interface{}, bool) {
	hc, err := c.GetOrDial(user, hop)
	if err != nil {
		return nil, false
	}
	leaf, err := sshclient.LeafSSHFromClient(hc)
	if err != nil {
		return nil, false
	}
	return leaf, true
}
