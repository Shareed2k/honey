package honeyprovider

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/shareed2k/honey/internal/hostexec"
)

// maxUnixSocketPathLen is the smaller of the darwin (104) and linux (108)
// sun_path limits; a local socket path at or beyond it cannot be bound.
const maxUnixSocketPathLen = 104

// StartLocalSocketForward listens on a local unix socket and forwards every
// accepted connection to remoteSocket on the upstream host via the mesh proxy
// (encoded as a "unix:<path>" tunnel target; the server opens an OpenSSH
// direct-streamlocal channel). stop closes the listener and removes the local
// socket file. It mirrors StartLocalForward but for unix sockets, which
// listenAndPipe (tcp-only) cannot express.
func (c *Client) StartLocalSocketForward(ctx context.Context, localSocket, remoteSocket string) (string, func(), error) {
	if !filepath.IsAbs(localSocket) {
		return "", nil, fmt.Errorf("honeyprovider: local socket must be absolute: %q", localSocket)
	}
	if !filepath.IsAbs(remoteSocket) {
		return "", nil, fmt.Errorf("honeyprovider: remote socket must be absolute: %q", remoteSocket)
	}
	if len(localSocket) >= maxUnixSocketPathLen {
		return "", nil, fmt.Errorf("honeyprovider: local socket path too long (%d >= %d bytes): %q", len(localSocket), maxUnixSocketPathLen, localSocket)
	}
	_ = os.Remove(localSocket) // clear a stale socket file; benign if absent
	ln, err := net.Listen("unix", localSocket)
	if err != nil {
		return "", nil, fmt.Errorf("honeyprovider: listen unix %s: %w", localSocket, err)
	}

	target := hostexec.FormatUnixTarget(remoteSocket)
	dial := c.dialer()
	runCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			local, aerr := ln.Accept()
			if aerr != nil {
				return // listener closed by stop()
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { _ = local.Close() }()
				up, derr := dial(runCtx, target)
				if derr != nil {
					return
				}
				defer func() { _ = up.Close() }()
				pipe(runCtx, local, up)
			}()
		}
	}()
	stop := sync.OnceFunc(func() {
		cancel()
		_ = ln.Close()
		wg.Wait()
		_ = os.Remove(localSocket)
	})
	return localSocket, stop, nil
}
