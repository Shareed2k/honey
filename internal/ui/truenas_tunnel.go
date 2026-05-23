package ui

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/sshclient"
	"github.com/shareed2k/honey/internal/truenasshell"
)

//go:embed honey_tcp_dial_bridge.py
var honeyTCPDialBridgePy string

const (
	truenasTunnelReadyTimeout = 45 * time.Second
	truenasShellDrainMax      = 8 * time.Second
	truenasShellDrainIdle     = 500 * time.Millisecond
)

// RunTrueNASTunnel listens locally and forwards each connection through the TrueNAS API shell
// dial bridge into the guest at remoteHost:remotePort (as seen from inside the guest).
func RunTrueNASTunnel(ctx context.Context, _ string, r hosts.Record, localFwd string, out io.Writer) error {
	if !hostexec.TruenasTunnelUsesAPIShell(r) {
		return fmt.Errorf("truenas tunnel: record does not use API shell transport")
	}
	localPort, remoteHost, remotePort, err := sshclient.ParseLocalForward(localFwd)
	if err != nil {
		return err
	}
	b, ok := hostexec.TrueNASBackendByName(r.Meta["backend_name"])
	if !ok {
		return fmt.Errorf("truenas backend not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	shellSess, err := truenasshell.OpenSession(ctx, b, r, 24, 120)
	if err != nil {
		return err
	}

	prOut, pwOut := io.Pipe()
	prIn, pwIn := io.Pipe()
	bridgeCtx, cancel := context.WithCancel(ctx)

	pumpOutDone := make(chan struct{})
	pumpInDone := make(chan struct{})
	go pumpTrueNASShellToPipe(shellSess, pwOut, pumpOutDone)
	go pumpPipeToTrueNASShell(prIn, shellSess, pumpInDone)

	brOut := bufio.NewReader(prOut)
	if err := drainAPIShellReader(bridgeCtx, brOut, truenasShellDrainMax, truenasShellDrainIdle); err != nil {
		cancel()
		_ = pwOut.Close()
		_ = pwIn.Close()
		<-pumpOutDone
		<-pumpInDone
		_ = shellSess.Close()
		return fmt.Errorf("truenas tunnel startup: %w", err)
	}

	pyB64 := base64.StdEncoding.EncodeToString([]byte(honeyTCPDialBridgePy))
	bootstrap := fmt.Sprintf(
		"\nstty -echo 2>/dev/null; HONEY_REMOTE_HOST=%s HONEY_REMOTE_PORT=%s printf %%s %s | base64 -d > /tmp/honey-tcp-dial-bridge.py && exec python3 -u /tmp/honey-tcp-dial-bridge.py\n",
		shellSingleQuoted(remoteHost), shellSingleQuoted(remotePort), shellSingleQuoted(pyB64),
	)
	if err := shellSess.WriteBinary([]byte(bootstrap)); err != nil {
		cancel()
		_ = pwOut.Close()
		_ = pwIn.Close()
		<-pumpOutDone
		<-pumpInDone
		_ = shellSess.Close()
		return fmt.Errorf("truenas tunnel bootstrap: %w", err)
	}

	readyCh := make(chan error, 1)
	go func() {
		_, err := readReadyLine(brOut)
		readyCh <- err
	}()

	stop := func() {
		cancel()
		_ = pwOut.Close()
		_ = pwIn.Close()
		<-pumpOutDone
		<-pumpInDone
		_ = shellSess.Close()
	}

	select {
	case err := <-readyCh:
		if err != nil {
			stop()
			return fmt.Errorf("truenas tunnel: %w", err)
		}
	case <-time.After(truenasTunnelReadyTimeout):
		stop()
		return errors.New("truenas tunnel: READY timeout")
	case <-ctx.Done():
		stop()
		return ctx.Err()
	}

	if out != nil {
		_, _ = fmt.Fprintf(out, "[%s] TrueNAS API tunnel: 127.0.0.1:%s -> %s:%s (Ctrl+C to stop)\n",
			time.Now().Format(time.RFC3339), localPort, remoteHost, remotePort)
	}

	bridgeErr := runTrueNASDialBridgeLoop(bridgeCtx, brOut, pwIn, localPort, remoteHost, remotePort, out)
	stop()
	if bridgeErr != nil && bridgeCtx.Err() == nil && !isPipeClosedErr(bridgeErr) {
		return bridgeErr
	}
	return bridgeCtx.Err()
}

// drainAPIShellReader discards PTY output until idleTimeout passes with no new data.
func drainAPIShellReader(ctx context.Context, br *bufio.Reader, maxWait, idleTimeout time.Duration) error {
	deadline := time.Now().Add(maxWait)
	idle := time.NewTimer(idleTimeout)
	defer idle.Stop()
	for {
		if time.Now().After(deadline) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-idle.C:
			return nil
		default:
		}
		if br.Buffered() > 0 {
			_, _ = br.Discard(br.Buffered())
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(idleTimeout)
			continue
		}
		readDone := make(chan error, 1)
		go func() {
			buf := make([]byte, 4096)
			_, err := br.Read(buf)
			readDone <- err
		}()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-idle.C:
			return nil
		case err := <-readDone:
			if err == nil {
				if !idle.Stop() {
					select {
					case <-idle.C:
					default:
					}
				}
				idle.Reset(idleTimeout)
				continue
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func runTrueNASDialBridgeLoop(
	ctx context.Context,
	brOut *bufio.Reader,
	pwIn io.Writer,
	localPort, remoteHost, remotePort string,
	out io.Writer,
) error {
	bind := net.JoinHostPort("127.0.0.1", localPort)
	ln, err := net.Listen("tcp", bind)
	if err != nil {
		return fmt.Errorf("listen %s: %w", bind, err)
	}
	defer func() { _ = ln.Close() }()

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	var nextCID atomic.Uint32
	nextCID.Store(1)
	conns := make(map[uint32]net.Conn)
	var connsMu sync.Mutex
	var stdinMu sync.Mutex

	closeConn := func(cid uint32) {
		connsMu.Lock()
		c := conns[cid]
		delete(conns, cid)
		connsMu.Unlock()
		if c != nil {
			_ = c.Close()
		}
	}

	readerDone := make(chan error, 1)
	go func() {
		for {
			typ, cid, payload, rerr := readHkvFrame(brOut)
			if rerr != nil {
				if isPipeClosedErr(rerr) || ctx.Err() != nil {
					readerDone <- nil
					return
				}
				readerDone <- fmt.Errorf("read frame: %w", rerr)
				return
			}
			switch typ {
			case hkvFrameData:
				connsMu.Lock()
				c := conns[cid]
				connsMu.Unlock()
				if c == nil {
					continue
				}
				if len(payload) > 0 {
					if _, werr := c.Write(payload); werr != nil {
						closeConn(cid)
						_ = writeHkvFrame(pwIn, &stdinMu, hkvFrameClose, cid, nil)
					}
				}
			case hkvFrameClose:
				closeConn(cid)
			}
		}
	}()

	acceptErrCh := make(chan error, 1)
	go func() {
		for {
			local, accErr := ln.Accept()
			if accErr != nil {
				if ctx.Err() != nil {
					acceptErrCh <- nil
					return
				}
				acceptErrCh <- accErr
				return
			}
			connsMu.Lock()
			if len(conns) >= hkvMaxOpenConns {
				connsMu.Unlock()
				_ = local.Close()
				continue
			}
			cid := nextCID.Add(1) - 1
			if cid == 0 {
				cid = nextCID.Add(1) - 1
			}
			conns[cid] = local
			connsMu.Unlock()

			if werr := writeHkvFrame(pwIn, &stdinMu, hkvFrameOpen, cid, nil); werr != nil {
				closeConn(cid)
				continue
			}
			if out != nil {
				_, _ = fmt.Fprintf(out, "[%s] Connection opened from %s -> %s:%s\n",
					time.Now().Format(time.RFC3339), local.RemoteAddr(), remoteHost, remotePort)
			}

			go func(cid uint32, local net.Conn) {
				defer func() {
					if out != nil {
						_, _ = fmt.Fprintf(out, "[%s] Connection closed from %s\n",
							time.Now().Format(time.RFC3339), local.RemoteAddr())
					}
					closeConn(cid)
					_ = writeHkvFrame(pwIn, &stdinMu, hkvFrameClose, cid, nil)
				}()
				buf := make([]byte, 64*1024)
				for {
					n, rerr := local.Read(buf)
					if n > 0 {
						if werr := writeHkvFrame(pwIn, &stdinMu, hkvFrameData, cid, buf[:n]); werr != nil {
							return
						}
					}
					if rerr != nil {
						return
					}
				}
			}(cid, local)
		}
	}()

	select {
	case <-ctx.Done():
		connsMu.Lock()
		for _, c := range conns {
			_ = c.Close()
		}
		connsMu.Unlock()
		<-readerDone
		return ctx.Err()
	case err := <-acceptErrCh:
		rerr := <-readerDone
		if err != nil {
			return err
		}
		return rerr
	}
}

// CanTrueNASTunnel reports whether the Tunnel action can run for this TrueNAS row.
func CanTrueNASTunnel(r hosts.Record) bool {
	if r.Provider != "truenas" {
		return false
	}
	if hosts.PrimaryIPTrimmed(r) != "" {
		return true
	}
	return hosts.IsTrueNASAPIShellRecord(r)
}
