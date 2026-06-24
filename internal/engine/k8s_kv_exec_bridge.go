package engine

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shareed2k/honey/internal/stepkv"
)

//go:embed k8s_kv_exec_bridge_pod.py
var k8sKVExecBridgePodPy string

type hkvFrameKind byte

const (
	hkvFrameData  hkvFrameKind = 0
	hkvFrameClose hkvFrameKind = 1
	hkvFrameOpen  hkvFrameKind = 2
)

const (
	hkvMaxFramePayload = 16 << 20
	hkvMaxOpenConns    = 256
	hkvConnInboxSize   = 32
	hkvDialTimeout     = 15 * time.Second
)

// hkvConn is the bridge's per-stream handle. The owning goroutine (runHkvConn) reads inbox and writes to
// the dialed stepkv socket; cancel tears that goroutine (and its read pump) down.
type hkvConn struct {
	inbox  chan []byte
	cancel context.CancelFunc
}

func readHkvFrame(r io.Reader) (typ hkvFrameKind, cid uint32, payload []byte, err error) {
	var hdr [9]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return 0, 0, nil, err
	}
	typ = hkvFrameKind(hdr[0])
	cid = binary.BigEndian.Uint32(hdr[1:5])
	ln := binary.BigEndian.Uint32(hdr[5:9])
	if ln > hkvMaxFramePayload {
		return 0, 0, nil, fmt.Errorf("kv bridge: frame too large (%d)", ln)
	}
	if ln == 0 {
		return typ, cid, nil, nil
	}
	payload = make([]byte, ln)
	if _, err = io.ReadFull(r, payload); err != nil {
		return 0, 0, nil, err
	}
	return typ, cid, payload, nil
}

// writeHkvFrame serializes one frame in a single Write so the io.Pipe doesn't fragment header from payload.
func writeHkvFrame(w io.Writer, mu *sync.Mutex, typ hkvFrameKind, cid uint32, payload []byte) error {
	payLen := len(payload)
	if payLen > hkvMaxFramePayload {
		return errors.New("kv bridge: payload too large")
	}
	buf := make([]byte, 9+payLen)
	buf[0] = byte(typ)
	binary.BigEndian.PutUint32(buf[1:5], cid)
	binary.BigEndian.PutUint32(buf[5:9], uint32(payLen)) // #nosec G115 -- payLen bounded by hkvMaxFramePayload (16MiB), fits uint32
	if payLen > 0 {
		copy(buf[9:], payload)
	}
	if mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
	_, err := w.Write(buf)
	return err
}

// startK8sRecipeKVExecBridge runs a long-lived kubectl exec with the embedded Python multiplexer so pod-local
// HTTP clients can reach the operator-side stepkv TCP listener. Returns env vars and stop, which cancels the
// exec context and waits for the bridge goroutine to finish.
func startK8sRecipeKVExecBridge(ctx context.Context, k8c *K8sNativeClient, sess *stepkv.Session) (map[string]string, func(), error) {
	if k8c == nil {
		return nil, nil, errors.New("kv bridge: nil k8s client")
	}
	if sess == nil {
		return nil, nil, errors.New("kv bridge: nil stepkv session")
	}
	local := sess.LocalTCPAddr()
	if local == "" {
		return nil, nil, errors.New("kv bridge: empty stepkv dial address")
	}

	pyB64 := base64.StdEncoding.EncodeToString([]byte(k8sKVExecBridgePodPy))
	// Avoid huge argv: stage script via base64, then exec python so the shell pid is replaced.
	bootstrap := fmt.Sprintf(`set -e
printf %%s %s | base64 -d > /tmp/honey-kv-bridge.py
exec python3 -u /tmp/honey-kv-bridge.py
`, ShellSingleQuoted(pyB64))

	execCtx, cancel := context.WithCancel(ctx)

	prOut, pwOut := io.Pipe()
	prIn, pwIn := io.Pipe()

	execErr := make(chan error, 1)
	go func() {
		execErr <- k8c.ExecInPod(execCtx, []string{"sh", "-c", bootstrap}, prIn, pwOut, io.Discard, false, nil)
	}()

	ready := make(chan int, 1)
	bridgeErrCh := make(chan error, 1)
	bridgeDone := make(chan struct{})

	go runHkvBridgeLoop(execCtx, prOut, pwIn, local, ready, bridgeErrCh, bridgeDone)

	stop := func() {
		cancel()
		_ = pwOut.Close()
		<-bridgeDone
		_ = pwIn.Close()
		select {
		case <-execErr:
		case <-time.After(5 * time.Second):
		}
	}

	select {
	case port := <-ready:
		env := map[string]string{
			"HONEY_KV_URL":   fmt.Sprintf("http://127.0.0.1:%d", port),
			"HONEY_KV_TOKEN": sess.Token(),
		}
		return env, stop, nil

	case err := <-execErr:
		cancel()
		_ = pwOut.Close()
		<-bridgeDone
		_ = pwIn.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("kv bridge: exec: %w", err)
		}
		if e := drainErr(bridgeErrCh); e != nil {
			return nil, nil, e
		}
		return nil, nil, errors.New("kv bridge: exec exited before READY")

	case <-bridgeDone:
		cancel()
		_ = pwOut.Close()
		_ = pwIn.Close()
		<-execErr
		if e := drainErr(bridgeErrCh); e != nil {
			return nil, nil, e
		}
		return nil, nil, errors.New("kv bridge: bridge exited before READY")

	case <-time.After(45 * time.Second):
		stop()
		return nil, nil, errors.New("kv bridge: READY timeout")
	}
}

func runHkvBridgeLoop(ctx context.Context, prOut io.Reader, pwIn io.Writer, dialAddr string, ready chan<- int, bridgeErrCh chan<- error, bridgeDone chan<- struct{}) {
	defer close(bridgeDone)
	conns := make(map[uint32]*hkvConn)
	var connsMu sync.Mutex
	defer func() {
		connsMu.Lock()
		for _, e := range conns {
			e.cancel()
		}
		connsMu.Unlock()
	}()

	fail := func(err error) {
		select {
		case bridgeErrCh <- err:
		default:
		}
	}

	br := bufio.NewReader(prOut)
	port, perr := readReadyLine(br)
	if perr != nil {
		fail(perr)
		return
	}
	ready <- port

	var stdinMu sync.Mutex
	closedByUs := make(map[uint32]struct{})
	for {
		typ, cid, payload, rerr := readHkvFrame(br)
		if rerr != nil {
			if !isPipeClosedErr(rerr) {
				fail(fmt.Errorf("kv bridge: read frame: %w", rerr))
			}
			return
		}
		dispatchHkvFrame(ctx, &connsMu, conns, closedByUs, &stdinMu, pwIn, dialAddr, typ, cid, payload, fail)
	}
}

func readReadyLine(br *bufio.Reader) (int, error) {
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return 0, fmt.Errorf("kv bridge: read READY: %w", err)
		}
		if port, ok := parseReadyPort(line); ok {
			return port, nil
		}
	}
}

const readyLinePrefix = "READY "

// stripKVBridgeLine removes common terminal escape sequences from a PTY line.
func stripKVBridgeLine(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\x1b' {
			b.WriteByte(s[i])
			continue
		}
		i++
		if i >= len(s) {
			break
		}
		switch s[i] {
		case '[':
			i++
			for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == ';' || s[i] == '?') {
				i++
			}
			if i < len(s) {
				i++
			}
		case ']':
			i++
			for i < len(s) && s[i] != '\x07' {
				i++
			}
		default:
			b.WriteByte('\x1b')
			b.WriteByte(s[i])
		}
	}
	return strings.TrimSpace(b.String())
}

func parseReadyPort(line string) (int, bool) {
	line = stripKVBridgeLine(line)
	idx := strings.Index(line, readyLinePrefix)
	if idx < 0 {
		return 0, false
	}
	portStr := strings.TrimSpace(line[idx+len(readyLinePrefix):])
	if end := strings.IndexFunc(portStr, func(r rune) bool { return r < '0' || r > '9' }); end >= 0 {
		portStr = portStr[:end]
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return 0, false
	}
	return port, true
}

func dispatchHkvFrame(ctx context.Context, connsMu *sync.Mutex, conns map[uint32]*hkvConn, closedByUs map[uint32]struct{}, stdinMu *sync.Mutex, pwIn io.Writer, dialAddr string, typ hkvFrameKind, cid uint32, payload []byte, fail func(error)) {
	switch typ {
	case hkvFrameOpen:
		connsMu.Lock()
		if len(conns) >= hkvMaxOpenConns {
			connsMu.Unlock()
			_ = writeHkvFrame(pwIn, stdinMu, hkvFrameClose, cid, nil)
			return
		}
		cctx, ccancel := context.WithCancel(ctx)
		e := &hkvConn{
			inbox:  make(chan []byte, hkvConnInboxSize),
			cancel: ccancel,
		}
		conns[cid] = e
		connsMu.Unlock()
		go runHkvConn(cctx, dialAddr, cid, e, stdinMu, pwIn, connsMu, conns, closedByUs, fail)

	case hkvFrameData:
		if len(payload) == 0 {
			return
		}
		connsMu.Lock()
		e := conns[cid]
		connsMu.Unlock()
		if e == nil {
			return
		}
		select {
		case e.inbox <- payload:
		default:
			// inbox full: backpressure → tear the conn down and tell the pod
			connsMu.Lock()
			_, alreadyClosed := closedByUs[cid]
			if !alreadyClosed {
				closedByUs[cid] = struct{}{}
				delete(conns, cid)
			}
			connsMu.Unlock()
			e.cancel()
			if !alreadyClosed {
				_ = writeHkvFrame(pwIn, stdinMu, hkvFrameClose, cid, nil)
			}
		}

	case hkvFrameClose:
		connsMu.Lock()
		e := conns[cid]
		delete(conns, cid)
		closedByUs[cid] = struct{}{}
		connsMu.Unlock()
		if e != nil {
			e.cancel()
		}
	}
}

func drainErr(ch <-chan error) error {
	select {
	case e := <-ch:
		return e
	default:
		return nil
	}
}

// runHkvConn owns one multiplexed connection end-to-end: it dials stepkv (cancellable via ctx), spawns a
// pump goroutine for stepkv → pod, and drains inbox writing pod → stepkv. ctx cancel closes the dialed
// conn via context.AfterFunc so both directions unwind even if the bridge read loop is wedged.
func runHkvConn(ctx context.Context, dialAddr string, cid uint32, e *hkvConn, stdinMu *sync.Mutex, pwIn io.Writer, connsMu *sync.Mutex, conns map[uint32]*hkvConn, closedByUs map[uint32]struct{}, fail func(error)) {
	defer func() {
		connsMu.Lock()
		_, alreadyClosed := closedByUs[cid]
		if !alreadyClosed {
			closedByUs[cid] = struct{}{}
			delete(conns, cid)
		}
		connsMu.Unlock()
		if !alreadyClosed {
			_ = writeHkvFrame(pwIn, stdinMu, hkvFrameClose, cid, nil)
		}
	}()

	d := net.Dialer{Timeout: hkvDialTimeout}
	c, err := d.DialContext(ctx, "tcp", dialAddr)
	if err != nil {
		if ctx.Err() == nil {
			fail(fmt.Errorf("kv bridge: dial stepkv: %w", err))
		}
		return
	}
	defer c.Close()

	stopAfter := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stopAfter()

	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		buf := make([]byte, 64*1024)
		for {
			n, rerr := c.Read(buf)
			if n > 0 {
				if werr := writeHkvFrame(pwIn, stdinMu, hkvFrameData, cid, buf[:n]); werr != nil {
					return
				}
			}
			if rerr != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			<-pumpDone
			return
		case payload := <-e.inbox:
			if _, werr := c.Write(payload); werr != nil {
				_ = c.Close()
				<-pumpDone
				return
			}
		}
	}
}

func isPipeClosedErr(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, net.ErrClosed)
}
