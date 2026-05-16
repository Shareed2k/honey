package hosts

import (
	"strconv"
	"strings"
)

const (
	metaKeySSHPort         = "ssh_port"
	metaKeySSHIdentityFile = "ssh_identity_file"
)

// MetaSSHPort returns the TCP port from r.Meta["ssh_port"] when it is a valid
// decimal integer in 1..65535.
func MetaSSHPort(r *Record) (port int, ok bool) {
	if r == nil || r.Meta == nil {
		return 0, false
	}
	s := strings.TrimSpace(r.Meta[metaKeySSHPort])
	if s == "" {
		return 0, false
	}
	p, err := strconv.Atoi(s)
	if err != nil || p <= 0 || p > 65535 {
		return 0, false
	}
	return p, true
}

// CloneWithMetaSSHPort returns a shallow copy of r with meta["ssh_port"] set to
// the decimal string for port (used by cue-exec to apply recipe-level ports).
func CloneWithMetaSSHPort(r Record, port int) Record {
	nr := r
	nr.Meta = cloneMetaStringMap(r.Meta)
	nr.Meta[metaKeySSHPort] = strconv.Itoa(port)
	return nr
}

// MetaSSHIdentityFile returns the private key path from r.Meta["ssh_identity_file"] when set
// (recipe-level ssh_private_key applied by cue-exec; not inventory search meta).
func MetaSSHIdentityFile(r *Record) (path string, ok bool) {
	if r == nil || r.Meta == nil {
		return "", false
	}
	path = strings.TrimSpace(r.Meta[metaKeySSHIdentityFile])
	if path == "" {
		return "", false
	}
	return path, true
}

// CloneWithMetaSSHIdentityFile returns a shallow copy of r with meta["ssh_identity_file"] set.
func CloneWithMetaSSHIdentityFile(r Record, path string) Record {
	nr := r
	nr.Meta = cloneMetaStringMap(r.Meta)
	nr.Meta[metaKeySSHIdentityFile] = strings.TrimSpace(path)
	return nr
}

func cloneMetaStringMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}
