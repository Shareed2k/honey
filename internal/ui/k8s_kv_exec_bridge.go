package ui

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
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

const (
	hkvFrameData  byte = 0
	hkvFrameClose byte = 1
	hkvFrameOpen  byte = 2
)

const hkvMaxFramePayload = 16 << 20

func readHkvFrame(r io.Reader) (typ byte, cid uint32, payload []byte, err error) {
	var hdr [9]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return 0, 0, nil, err
	}
	typ = hdr[0]
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

func writeHkvFrame(w io.Writer, mu *sync.Mutex, typ byte, cid uint32, payload []byte) error {
	payLen := len(payload)
	if payLen > hkvMaxFramePayload {
		return fmt.Errorf("kv bridge: payload too large")
	}
	var hdr [9]byte
	hdr[0] = typ
	binary.BigEndian.PutUint32(hdr[1:5], cid)
	binary.BigEndian.PutUint32(hdr[5:9], uint32(payLen)) // #nosec G115 -- payLen <= hkvMaxFramePayload << 2^32-1
	if mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	_, err := w.Write(payload)
	return err
}

// startK8sRecipeKVExecBridge runs a long-lived kubectl exec with the embedded Python multiplexer so pod-local
// HTTP clients can reach the operator-side stepkv TCP listener. Returns env vars and stop, which cancels the
// exec context and waits for the bridge goroutine to finish.
func startK8sRecipeKVExecBridge(ctx context.Context, k8c *k8sNativeClient, sess *stepkv.Session) (map[string]string, func(), error) {
	if k8c == nil {
		return nil, nil, fmt.Errorf("kv bridge: nil k8s client")
	}
	if sess == nil {
		return nil, nil, fmt.Errorf("kv bridge: nil stepkv session")
	}
	local := sess.LocalTCPAddr()
	if local == "" {
		return nil, nil, fmt.Errorf("kv bridge: empty stepkv dial address")
	}

	pyB64 := base64.StdEncoding.EncodeToString([]byte(k8sKVExecBridgePodPy))
	// Write script to a stable path then exec python (avoids huge argv / env limits).
	bootstrap := fmt.Sprintf(`set -e
printf %%s %s | base64 -d > /tmp/honey-kv-bridge.py
exec python3 -u /tmp/honey-kv-bridge.py
`, shellSingleQuoted(pyB64))

	execCtx, cancel := context.WithCancel(ctx)

	prOut, pwOut := io.Pipe()
	prIn, pwIn := io.Pipe()

	execErr := make(chan error, 1)
	go func() {
		execErr <- k8c.execInPod(execCtx, []string{"sh", "-c", bootstrap}, prIn, pwOut, io.Discard, false, nil)
	}()

	ready := make(chan int, 1)
	bridgeDone := make(chan struct{})
	var bridgeErr error
	var bridgeErrMu sync.Mutex

	go func() {
		defer close(bridgeDone)
		conns := make(map[uint32]net.Conn)
		var connsMu sync.Mutex
		defer func() {
			connsMu.Lock()
			for _, c := range conns {
				_ = c.Close()
			}
			connsMu.Unlock()
		}()

		br := bufio.NewReader(prOut)
		line, err := br.ReadString('\n')
		if err != nil {
			bridgeErrMu.Lock()
			bridgeErr = fmt.Errorf("kv bridge: read READY: %w", err)
			bridgeErrMu.Unlock()
			return
		}
		line = strings.TrimSpace(line)
		const pfx = "READY "
		if !strings.HasPrefix(line, pfx) {
			bridgeErrMu.Lock()
			bridgeErr = fmt.Errorf("kv bridge: bad READY line %q", line)
			bridgeErrMu.Unlock()
			return
		}
		portStr := strings.TrimSpace(strings.TrimPrefix(line, pfx))
		port, err := strconv.Atoi(portStr)
		if err != nil || port <= 0 || port > 65535 {
			bridgeErrMu.Lock()
			bridgeErr = fmt.Errorf("kv bridge: bad READY port %q", portStr)
			bridgeErrMu.Unlock()
			return
		}
		ready <- port

		var stdinMu sync.Mutex

		dialStepkv := func() (net.Conn, error) {
			return net.DialTimeout("tcp", local, 15*time.Second)
		}

		for {
			typ, cid, payload, rerr := readHkvFrame(br)
			if rerr != nil {
				if rerr == io.EOF || errorsIsClosedPipe(rerr) {
					return
				}
				bridgeErrMu.Lock()
				if bridgeErr == nil {
					bridgeErr = fmt.Errorf("kv bridge: read frame: %w", rerr)
				}
				bridgeErrMu.Unlock()
				return
			}
			switch typ {
			case hkvFrameOpen:
				c, derr := dialStepkv()
				if derr != nil {
					bridgeErrMu.Lock()
					if bridgeErr == nil {
						bridgeErr = fmt.Errorf("kv bridge: dial stepkv: %w", derr)
					}
					bridgeErrMu.Unlock()
					_ = writeHkvFrame(pwIn, &stdinMu, hkvFrameClose, cid, nil)
					continue
				}
				connsMu.Lock()
				conns[cid] = c
				connsMu.Unlock()
				go pumpStepkvToPod(execCtx, &stdinMu, pwIn, cid, c)

			case hkvFrameData:
				connsMu.Lock()
				c := conns[cid]
				connsMu.Unlock()
				if c == nil {
					continue
				}
				if len(payload) > 0 {
					_, _ = c.Write(payload)
				}

			case hkvFrameClose:
				connsMu.Lock()
				c := conns[cid]
				delete(conns, cid)
				connsMu.Unlock()
				if c != nil {
					_ = c.Close()
				}

			default:
				// ignore unknown
			}
		}
	}()

	select {
	case port := <-ready:
		env := map[string]string{
			"HONEY_KV_URL":   fmt.Sprintf("http://127.0.0.1:%d", port),
			"HONEY_KV_TOKEN": sess.Token(),
		}
		stop := func() {
			cancel()
			_ = pwOut.Close()
			<-bridgeDone
			_ = pwIn.Close()
			<-execErr
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
		bridgeErrMu.Lock()
		e := bridgeErr
		bridgeErrMu.Unlock()
		if e != nil {
			return nil, nil, e
		}
		return nil, nil, fmt.Errorf("kv bridge: exec exited before READY")

	case <-bridgeDone:
		cancel()
		_ = pwOut.Close()
		_ = pwIn.Close()
		<-execErr
		bridgeErrMu.Lock()
		e := bridgeErr
		bridgeErrMu.Unlock()
		if e != nil {
			return nil, nil, e
		}
		return nil, nil, fmt.Errorf("kv bridge: bridge exited before READY")

	case <-time.After(45 * time.Second):
		cancel()
		_ = pwOut.Close()
		<-bridgeDone
		_ = pwIn.Close()
		<-execErr
		return nil, nil, fmt.Errorf("kv bridge: READY timeout")
	}
}

func pumpStepkvToPod(ctx context.Context, stdinMu *sync.Mutex, pwIn io.Writer, cid uint32, c net.Conn) {
	defer func() { _ = c.Close() }()
	defer func() { _ = writeHkvFrame(pwIn, stdinMu, hkvFrameClose, cid, nil) }()

	buf := make([]byte, 64*1024)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, err := c.Read(buf)
		if n > 0 {
			if werr := writeHkvFrame(pwIn, stdinMu, hkvFrameData, cid, buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func errorsIsClosedPipe(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "file already closed") || strings.Contains(s, "use of closed network connection") || strings.Contains(s, "broken pipe")
}
