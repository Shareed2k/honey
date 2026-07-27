package honeyprovider

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/sshclient"
	"golang.org/x/term"
)

// runInteractiveWS is the testable core of Executor.RunInteractive: it
// speaks the /ws/ssh protocol (internal/webserver/ws_ssh.go) over a
// WebSocket, with no dependency on a real TTY so it can be driven by
// in-memory in/out in tests.
//
// It dials baseURL (a base "http(s)://..." URL, rewritten to "ws(s)://..."
// with "/ws/ssh" appended) via dialWS using the same transport helpers
// (token/tlsCfg/dial) the rest of this package's upstream calls use, sends
// hello, then:
//   - pumps in -> WS BinaryMessage (stdin)
//   - pumps WS BinaryMessage -> out (stdout/stderr)
//   - pumps resize -> WS TextMessage {"type":"resize","cols":N,"rows":N}
//   - returns nil on WS TextMessage {"closed":true}, or an error on
//     {"error":"..."} / an unexpected WS read error.
//
// Every goroutine it starts exits when the session ends, but they are not
// all joined before returning. The resize pump observes ctx cancellation
// promptly (it only ever blocks in a select over ctx.Done()/the resize
// channel) and is joined via wg before return. The reader goroutine's exit
// is joined synchronously via readDone before runInteractiveWS returns. The
// stdin pump is fire-and-forget and deliberately NOT joined: it can only
// notice ctx cancellation between reads of in, and production callers pass
// os.Stdin, whose blocked Read cannot be interrupted by ctx cancellation or
// by closing the WS conn -- only the next keystroke unblocks it. Waiting for
// it here would hang RunInteractive after every clean session end (server
// sends {"closed":true} when the user types "exit") until the user pressed
// one more key. This matches Client.RunWithStreams (exec.go), which also
// never joins its stdin-pump goroutine: on session end it leaves that
// goroutine to exit on its own once its next write fails, harmless for the
// remaining process lifetime.
func runInteractiveWS(
	ctx context.Context,
	baseURL string,
	hello map[string]any,
	in io.Reader,
	out io.Writer,
	token string,
	tlsCfg *tls.Config,
	dial func(ctx context.Context, network, addr string) (net.Conn, error),
	resize <-chan [2]int,
) error {
	wsURL := strings.Replace(baseURL, "http", "ws", 1) + "/ws/ssh"
	conn, err := dialWS(ctx, wsURL, token, tlsCfg, dial)
	if err != nil {
		return fmt.Errorf("dial /ws/ssh: %w", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(hello); err != nil {
		return fmt.Errorf("send hello: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// gorilla/websocket connections do not support concurrent writers; the
	// stdin pump (Binary) and the resize pump (Text) both write to conn, so
	// serialize them.
	var writeMu sync.Mutex
	writeMessage := func(mt int, p []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteMessage(mt, p)
	}

	var wg sync.WaitGroup

	// stdin pump: in -> WS BinaryMessage. Exits on a read error/EOF from in,
	// or (between reads) when ctx is cancelled. Fire-and-forget: NOT added to
	// wg, and runInteractiveWS does not wait for it (see the doc comment
	// above) so a blocked in.Read (os.Stdin in production) never delays the
	// prompt return on session end. On return, conn is closed (defer above)
	// and ctx is cancelled, so once this goroutine does get its next chance
	// to run -- the next keystroke unblocks in.Read in production, or ctx.Done
	// fires between reads -- its writeMessage/ctx check fails and it exits.
	go func() {
		buf := make([]byte, 32*1024)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			n, rerr := in.Read(buf)
			if n > 0 {
				if werr := writeMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if rerr != nil {
				return
			}
		}
	}()

	// resize pump: resize -> WS TextMessage {"type":"resize",...}.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case sz, ok := <-resize:
				if !ok {
					return
				}
				payload, merr := json.Marshal(map[string]any{"type": "resize", "cols": sz[0], "rows": sz[1]})
				if merr != nil {
					continue
				}
				if werr := writeMessage(websocket.TextMessage, payload); werr != nil {
					return
				}
			}
		}
	}()

	// reader: WS -> out. This goroutine is the session's authoritative
	// termination signal: it ends on {"closed":true}/{"error":...} or a WS
	// read error, and readDone is joined synchronously below (no wg needed).
	readDone := make(chan error, 1)
	go func() {
		for {
			mt, p, rerr := conn.ReadMessage()
			if rerr != nil {
				if websocket.IsCloseError(rerr, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					readDone <- nil
				} else {
					readDone <- rerr
				}
				return
			}
			switch mt {
			case websocket.BinaryMessage:
				if out != nil {
					if _, werr := out.Write(p); werr != nil {
						readDone <- werr
						return
					}
				}
			case websocket.TextMessage:
				var msg struct {
					Closed bool   `json:"closed"`
					Error  string `json:"error"`
				}
				if json.Unmarshal(p, &msg) == nil {
					if msg.Error != "" {
						readDone <- fmt.Errorf("remote error: %s", msg.Error)
						return
					}
					if msg.Closed {
						readDone <- nil
						return
					}
				}
				// Unrecognized text message: ignore and keep reading.
			}
		}
	}()

	runErr := <-readDone
	cancel()
	wg.Wait()
	return runErr
}

// honeyprovider.Executor satisfies the hostexec.InteractiveStreamer seam via
// RunInteractiveStreams (it forwards the session over the mesh; the upstream
// server dispatches to the right native shell) and hostexec.ProxyExecutor, so
// dispatchers route a mesh-resolved record to it wholesale before attempting any
// local provider console.
var (
	_ hostexec.InteractiveStreamer = (*Executor)(nil)
	_ hostexec.ProxyExecutor       = (*Executor)(nil)
)

// IsProxy reports that this executor forwards sessions to the upstream node that
// owns the record rather than executing them locally. ForRecord only resolves to
// a honeyprovider.Executor when this node has a matching honey backend for the
// record's routing tag, so it is always a proxy.
func (e *Executor) IsProxy() bool { return true }

// RunInteractiveStreams proxies an interactive terminal session for r through
// the upstream Honey server's /ws/ssh endpoint, carrying the supplied
// stdin/stdout and resize events over WebSocket frames. It is the
// stream-oriented core shared by the CLI RunInteractive (which adds a local PTY
// in raw mode + SIGWINCH forwarding on os.Stdin/os.Stdout) and the web terminal
// (which supplies the browser's stdin/stdout pipes and WS-driven resizes). The
// upstream server receives the full record and dispatches to the right native
// terminal (docker exec / k8s exec / ssh) on its side. resize carries
// [cols, rows] pairs. Cancel ctx (or let the server close the session) to
// unwind.
func (e *Executor) RunInteractiveStreams(ctx context.Context, user string, r hosts.Record, stdin io.Reader, stdout io.Writer, cols, rows int, resize <-chan [2]int) error {
	hello := map[string]any{
		"ssh_user": user,
		"record":   r,
		"cols":     cols,
		"rows":     rows,
	}

	tlsCfg, err := clientTLSConfig(e.Insecure, e.MTLS, e.ServerCA)
	if err != nil {
		return err
	}
	token := e.Token
	if e.MTLS {
		token = ""
	}

	return runInteractiveWS(ctx, e.URL, hello, stdin, stdout, token, tlsCfg, meshDialContext(e.Mesh, e.MeshAddr), resize)
}

// RunInteractive opens an interactive terminal session against r by proxying
// through the upstream Honey server's /ws/ssh endpoint: a client-side PTY
// (raw mode, initial size, SIGWINCH-driven resize forwarding) with
// stdin/stdout carried as WebSocket frames over RunInteractiveStreams.
func (e *Executor) RunInteractive(user string, r hosts.Record) error {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return fmt.Errorf("stdin is not a terminal")
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("set raw mode: %w", err)
	}
	defer func() { _ = term.Restore(fd, oldState) }()

	cols, rows, err := term.GetSize(fd)
	if err != nil {
		cols, rows = 80, 24
	}

	resizeCh := make(chan [2]int, 1)
	stopResize := sshclient.StartTerminalResize(fd, func(newCols, newRows int) {
		select {
		case resizeCh <- [2]int{newCols, newRows}:
		default:
		}
	})
	defer stopResize()

	return e.RunInteractiveStreams(context.Background(), user, r, os.Stdin, os.Stdout, cols, rows, resizeCh)
}
