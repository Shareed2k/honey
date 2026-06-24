package engine

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

	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/truenasprovider"
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

type trueNASBridgeTarget struct {
	Host string
	Port string
}

type trueNASBridge struct {
	ctx         context.Context
	cancel      context.CancelFunc
	sess        *truenasshell.Session
	brOut       *bufio.Reader
	pwOut       *io.PipeWriter
	pwIn        *io.PipeWriter
	pumpOutDone chan struct{}
	pumpInDone  chan struct{}
	stopOnce    sync.Once
}

func resolveTrueNASBridgeTarget(r hosts.Record, address string) trueNASBridgeTarget {
	remoteHost, remotePort, err := net.SplitHostPort(address)
	if err != nil {
		remoteHost = address
		remotePort = "80"
	}

	// When the bridge runs on the TrueNAS appliance, target loopback means the guest's IP.
	if r.Meta["kind"] != "appliance" {
		ip := r.PrimaryIPTrimmed()
		if ip != "" && (remoteHost == "localhost" || remoteHost == "127.0.0.1") {
			zap.L().Debug(
				"Translating upstream loopback address to guest PrimaryIP",
				zap.String("original", remoteHost),
				zap.String("guest_ip", ip),
			)
			remoteHost = ip
		}
	}

	return trueNASBridgeTarget{Host: remoteHost, Port: remotePort}
}

func trueNASApplianceRecord(r hosts.Record) hosts.Record {
	applianceRec := r
	meta := make(map[string]string, len(r.Meta)+1)
	for k, v := range r.Meta {
		meta[k] = v
	}
	meta["kind"] = "appliance"
	applianceRec.Meta = meta
	return applianceRec
}

func trueNASDialBridgeBootstrap(remoteHost, remotePort string) string {
	pyB64 := base64.StdEncoding.EncodeToString([]byte(honeyTCPDialBridgePy))
	return fmt.Sprintf(
		"\nstty raw -echo min 1 time 0 2>/dev/null; export HONEY_REMOTE_HOST=%s HONEY_REMOTE_PORT=%s; printf %%s %s | base64 -d > /tmp/honey-tcp-dial-bridge.py && exec python3 -u /tmp/honey-tcp-dial-bridge.py\n",
		ShellSingleQuoted(remoteHost), ShellSingleQuoted(remotePort), ShellSingleQuoted(pyB64),
	)
}

func startTrueNASBridge(ctx context.Context, b truenasprovider.TrueNASBackendRuntime, shellRec hosts.Record, targetName string, target trueNASBridgeTarget) (*trueNASBridge, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	shellSess, err := truenasshell.OpenSession(ctx, b, shellRec, 24, 120)
	if err != nil {
		return nil, err
	}
	zap.L().Debug(
		"TrueNAS API shell session opened",
		zap.String("target_name", targetName),
		zap.String("shell_kind", shellRec.Meta["kind"]),
		zap.String("backend_name", shellRec.Meta["backend_name"]),
	)

	prOut, pwOut := io.Pipe()
	prIn, pwIn := io.Pipe()
	bridgeCtx, cancel := context.WithCancel(ctx)
	pumpOutDone := make(chan struct{})
	pumpInDone := make(chan struct{})

	go pumpTrueNASShellToPipe(shellSess, pwOut, pumpOutDone)
	go pumpPipeToTrueNASShell(prIn, shellSess, pumpInDone)

	bridge := &trueNASBridge{
		ctx:         bridgeCtx,
		cancel:      cancel,
		sess:        shellSess,
		brOut:       bufio.NewReader(prOut),
		pwOut:       pwOut,
		pwIn:        pwIn,
		pumpOutDone: pumpOutDone,
		pumpInDone:  pumpInDone,
	}

	if err := drainAPIShellReader(bridgeCtx, bridge.brOut, truenasShellDrainMax, truenasShellDrainIdle); err != nil {
		bridge.Stop()
		return nil, fmt.Errorf("truenas bridge startup: %w", err)
	}

	zap.L().Debug(
		"Writing TrueNAS python dial bridge bootstrap",
		zap.String("target_name", targetName),
		zap.String("remote_host", target.Host),
		zap.String("remote_port", target.Port),
	)
	if err := shellSess.WriteBinary([]byte(trueNASDialBridgeBootstrap(target.Host, target.Port))); err != nil {
		bridge.Stop()
		return nil, fmt.Errorf("truenas bridge bootstrap: %w", err)
	}

	readyCh := make(chan error, 1)
	go func() {
		_, err := readReadyLine(bridge.brOut)
		readyCh <- err
	}()

	select {
	case err := <-readyCh:
		if err != nil {
			zap.L().Debug(
				"TrueNAS python dial bridge READY failed",
				zap.String("target_name", targetName),
				zap.Error(err),
			)
			bridge.Stop()
			return nil, fmt.Errorf("truenas bridge: %w", err)
		}
		zap.L().Debug("TrueNAS python dial bridge ready", zap.String("target_name", targetName))
	case <-time.After(truenasTunnelReadyTimeout):
		zap.L().Debug("TrueNAS python dial bridge READY timeout", zap.String("target_name", targetName))
		bridge.Stop()
		return nil, errors.New("truenas bridge: READY timeout")
	case <-bridgeCtx.Done():
		bridge.Stop()
		return nil, bridgeCtx.Err()
	}

	return bridge, nil
}

func (b *trueNASBridge) Stop() {
	b.stopOnce.Do(func() {
		b.cancel()
		_ = b.pwOut.Close()
		_ = b.pwIn.Close()
		_ = b.sess.Close()
		<-b.pumpOutDone
		<-b.pumpInDone
	})
}

// DialTrueNASUpstream provides an in-memory net.Conn proxied over the API shell.
// DialTrueNASUpstream ...
func DialTrueNASUpstream(ctx context.Context, _ string, r hosts.Record, address string) (net.Conn, error) {
	zap.L().Debug(
		"Dialing TrueNAS upstream",
		zap.String("target_name", r.Name),
		zap.String("target_kind", r.Meta["kind"]),
		zap.String("backend_name", r.Meta["backend_name"]),
		zap.String("address", address),
	)

	if r.Provider != "truenas" || !r.IsTrueNASAPIShell() {
		return nil, fmt.Errorf("truenas dial: record does not support API shell")
	}
	b, ok := truenasprovider.BackendByName(r.Meta["backend_name"])
	if !ok {
		return nil, fmt.Errorf("truenas backend not configured")
	}

	target := resolveTrueNASBridgeTarget(r, address)
	zap.L().Debug(
		"TrueNAS upstream bridge target resolved",
		zap.String("target_name", r.Name),
		zap.String("remote_host", target.Host),
		zap.String("remote_port", target.Port),
	)

	// Always establish app proxy bridges on the TrueNAS host appliance. That keeps the
	// Python bridge out of VM serial consoles and minimal app containers.
	bridge, err := startTrueNASBridge(ctx, b, trueNASApplianceRecord(r), r.Name, target)
	if err != nil {
		return nil, fmt.Errorf("truenas dial: %w", err)
	}

	const cid = 1
	if err := writeHkvFrame(bridge.pwIn, nil, hkvFrameOpen, cid, nil); err != nil {
		bridge.Stop()
		return nil, fmt.Errorf("truenas dial connect: %w", err)
	}
	zap.L().Debug(
		"TrueNAS python dial bridge connection opened",
		zap.String("target_name", r.Name),
		zap.Uint32("cid", cid),
	)

	c := &truenasInMemConn{
		pwIn:  bridge.pwIn,
		brOut: bridge.brOut,
		stop:  bridge.Stop,
		cid:   cid,
	}

	return c, nil
}

type truenasInMemConn struct {
	pwIn      io.Writer
	brOut     *bufio.Reader
	stop      func()
	cid       uint32
	pending   []byte
	writeMu   sync.Mutex
	closeOnce sync.Once
}

func (c *truenasInMemConn) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	if len(c.pending) > 0 {
		n := copy(b, c.pending)
		c.pending = c.pending[n:]
		return n, nil
	}

	for {
		msgType, rcid, payload, err := readHkvFrame(c.brOut)
		if err != nil {
			return 0, err
		}
		if rcid != c.cid {
			continue
		}
		switch msgType {
		case hkvFrameClose:
			return 0, io.EOF
		case hkvFrameData:
			if len(payload) == 0 {
				continue
			}
			n := copy(b, payload)
			if n < len(payload) {
				c.pending = payload[n:]
			}
			return n, nil
		}
	}
}

func (c *truenasInMemConn) Write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	chunk := b
	for len(chunk) > 0 {
		take := len(chunk)
		if take > 8192 {
			take = 8192
		}
		if err := writeHkvFrame(c.pwIn, &c.writeMu, hkvFrameData, c.cid, chunk[:take]); err != nil {
			return 0, err
		}
		chunk = chunk[take:]
	}
	return len(b), nil
}

func (c *truenasInMemConn) Close() error {
	c.closeOnce.Do(func() {
		_ = writeHkvFrame(c.pwIn, &c.writeMu, hkvFrameClose, c.cid, nil)
		c.stop()
	})
	return nil
}

func (c *truenasInMemConn) LocalAddr() net.Addr                { return &net.TCPAddr{} }
func (c *truenasInMemConn) RemoteAddr() net.Addr               { return &net.TCPAddr{} }
func (c *truenasInMemConn) SetDeadline(_ time.Time) error      { return nil }
func (c *truenasInMemConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *truenasInMemConn) SetWriteDeadline(_ time.Time) error { return nil }

// RunTrueNASTunnel listens locally and forwards each connection through the TrueNAS API shell
// dial bridge into the guest at remoteHost:remotePort (as seen from inside the guest).
// RunTrueNASTunnel ...
func RunTrueNASTunnel(ctx context.Context, _ string, r hosts.Record, localFwd string, out io.Writer) error {
	if !truenasprovider.TruenasTunnelUsesAPIShell(r) {
		return fmt.Errorf("truenas tunnel: record does not use API shell transport")
	}
	localPort, remoteHost, remotePort, err := sshclient.ParseLocalForward(localFwd)
	if err != nil {
		return err
	}
	b, ok := truenasprovider.BackendByName(r.Meta["backend_name"])
	if !ok {
		return fmt.Errorf("truenas backend not configured")
	}
	bridge, err := startTrueNASBridge(ctx, b, r, r.Name, trueNASBridgeTarget{Host: remoteHost, Port: remotePort})
	if err != nil {
		return fmt.Errorf("truenas tunnel: %w", err)
	}

	if out != nil {
		_, _ = fmt.Fprintf(out, "[%s] TrueNAS API tunnel: 127.0.0.1:%s -> %s:%s (Ctrl+C to stop)\n",
			time.Now().Format(time.RFC3339), localPort, remoteHost, remotePort)
	}

	bridgeErr := runTrueNASDialBridgeLoop(bridge.ctx, bridge.brOut, bridge.pwIn, localPort, remoteHost, remotePort, out)
	bridge.Stop()
	if bridgeErr != nil && bridge.ctx.Err() == nil && !isPipeClosedErr(bridgeErr) {
		return bridgeErr
	}
	return bridge.ctx.Err()
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
// CanTrueNASTunnel ...
func CanTrueNASTunnel(r hosts.Record) bool {
	if r.Provider != "truenas" {
		return false
	}
	if r.PrimaryIPTrimmed() != "" {
		return true
	}
	return r.IsTrueNASAPIShell()
}
