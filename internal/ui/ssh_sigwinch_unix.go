//go:build !windows

package ui

import (
	"os"
	"os/signal"
	"sync"
	"syscall"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// startPTYResizeForwarding sends SIGWINCH-driven size updates to the remote PTY.
func startPTYResizeForwarding(fd int, sess *ssh.Session) (stop func()) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGWINCH)
	done := make(chan struct{})
	var once sync.Once
	go func() {
		defer signal.Stop(sig)
		for {
			select {
			case <-done:
				return
			case <-sig:
				w, h, err := term.GetSize(fd)
				if err != nil {
					continue
				}
				_ = sess.WindowChange(h, w)
			}
		}
	}()
	return func() {
		once.Do(func() {
			close(done)
			signal.Stop(sig)
		})
	}
}
