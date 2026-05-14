//go:build windows

package sshclient

import "golang.org/x/crypto/ssh"

// StartTerminalResize is a no-op on Windows (no SIGWINCH).
func StartTerminalResize(_ int, _ func(cols, rows int)) (stop func()) {
	return func() {}
}

func StartPTYResizeForwarding(_ int, _ *ssh.Session, _ func(cols, rows int)) (stop func()) {
	return func() {}
}
