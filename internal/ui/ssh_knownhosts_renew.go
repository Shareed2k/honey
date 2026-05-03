package ui

import (
	"fmt"
	"net"
	"os"
	"strings"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/shareed2k/honey/internal/safepath"
)

// honeySSHAutoRenewStaleHostKeys is true by default: when a known_hosts entry no longer matches the server,
// honey removes matching lines from writable known_hosts files (pure Go, same idea as ssh-keygen -R) and appends the new key.
// Set HONEY_SSH_RENEW_STALE_HOST_KEYS to 0/false/no/off to disable (then mismatches fail until you fix known_hosts manually).
// Any other value (including unset) leaves renewal on.
// This mirrors fixing a stale entry after a VM rebuild; it weakens MITM detection for that transition.
func honeySSHAutoRenewStaleHostKeys() bool {
	s := strings.ToLower(strings.TrimSpace(os.Getenv("HONEY_SSH_RENEW_STALE_HOST_KEYS")))
	if s == "0" || s == "false" || s == "no" || s == "off" {
		return false
	}
	return true
}

func knownHostsRemovalCandidates(hostname string, remote net.Addr) []string {
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		for _, x := range out {
			if x == s {
				return
			}
		}
		out = append(out, s)
	}
	if strings.TrimSpace(hostname) != "" {
		add(knownhosts.Normalize(hostname))
	}
	if remote != nil {
		add(knownhosts.Normalize(remote.String()))
	}
	return out
}

func canMutateKnownHostsFile(p string) bool {
	fi, err := safepath.Stat(p)
	if err != nil || fi.IsDir() {
		return false
	}
	return safepath.OpenReadWriteProbe(p) == nil
}

// removeHostFromKnownHostsFiles rewrites each writable known_hosts file to drop lines matching this dial
// (hostname + remote), mirroring ssh-keygen -R behavior without invoking an external binary.
func removeHostFromKnownHostsFiles(paths []string, hostname string, remote net.Addr) error {
	addrs, err := dialKhAddrs(hostname, remote)
	if err != nil {
		return err
	}
	for _, file := range paths {
		if !canMutateKnownHostsFile(file) {
			continue
		}
		if _, err := rewriteKnownHostsStrippingAddrs(file, addrs); err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
	}
	return nil
}
