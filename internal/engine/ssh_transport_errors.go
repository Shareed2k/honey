package engine

import (
	"errors"
	"io"
	"net"
	"strings"
	"syscall"
)

// IsSSHConnTransientError reports whether err is a transport-level failure that
// often clears after closing the TCP/SSH session and dialing again (stale
// cache entry, local routing/socket glitch, reset by peer).
// IsSSHConnTransientError ...
func IsSSHConnTransientError(err error) bool {
	if err == nil {
		return false
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Err != nil {
		var sys syscall.Errno
		if errors.As(opErr.Err, &sys) {
			switch sys {
			case syscall.EADDRNOTAVAIL, syscall.ECONNRESET, syscall.EPIPE, syscall.ETIMEDOUT, syscall.ENETUNREACH, syscall.EHOSTUNREACH:
				return true
			}
		}
		if errors.Is(opErr.Err, io.EOF) {
			return true
		}
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "can't assign requested address") ||
		strings.Contains(s, "cannot assign requested address") ||
		strings.Contains(s, "connection reset by peer") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "use of closed network connection") ||
		strings.Contains(s, "i/o timeout")
}
