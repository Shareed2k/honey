//go:build windows

package engine

import "golang.org/x/crypto/ssh"

func startPTYResizeForwarding(_ int, _ *ssh.Session, _ func(cols, rows int)) (stop func()) {
	return func() {}
}
