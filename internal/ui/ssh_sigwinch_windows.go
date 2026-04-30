//go:build windows

package ui

import "golang.org/x/crypto/ssh"

func startPTYResizeForwarding(_ int, _ *ssh.Session) (stop func()) {
	return func() {}
}
