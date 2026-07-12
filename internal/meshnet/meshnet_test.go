package meshnet

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
)

// resetState restores the package singleton to its zero state, both before
// and after each subtest, so subtests never leak state into one another.
// Every subtest in this file mutates package-level singleton state
// (mu/started/cur-equivalent) and so must run sequentially — not
// t.Parallel() — which is why this file does not call t.Parallel anywhere.
//
// Tests are split across several top-level Test functions purely to keep
// each function's cyclomatic complexity low (gocyclo); they are still all
// sequential subtests of the same singleton and still never call
// t.Parallel().
func resetState(t *testing.T) {
	t.Helper()
	_ = Stop(context.Background())
	t.Cleanup(func() {
		_ = Stop(context.Background())
	})
}

// withFakeNewHost swaps the package-level newHost seam for the duration of
// the calling subtest, restoring the real libp2p.New-backed one on cleanup.
func withFakeNewHost(t *testing.T, fn func(opts ...libp2p.Option) (meshHost, error)) {
	t.Helper()
	orig := newHost
	newHost = fn
	t.Cleanup(func() { newHost = orig })
}

func TestMeshnetBeforeStart(t *testing.T) {
	t.Run("enabled is false before any start call", func(t *testing.T) {
		resetState(t)

		if Enabled() {
			t.Fatal("expected Enabled() to be false before Start")
		}
	})

	t.Run("dial before start returns errNotStarted", func(t *testing.T) {
		resetState(t)

		if _, err := DialPeer(context.Background(), "/ip4/127.0.0.1/udp/1/quic-v1/p2p/"+newTestPeerID().String()); !errors.Is(err, errNotStarted) {
			t.Fatalf("DialPeer: got %v, want errNotStarted", err)
		}
		if _, err := Listener(); !errors.Is(err, errNotStarted) {
			t.Fatalf("Listener: got %v, want errNotStarted", err)
		}
		if _, err := Status(); !errors.Is(err, errNotStarted) {
			t.Fatalf("Status: got %v, want errNotStarted", err)
		}
	})
}

func TestMeshnetStartNoOp(t *testing.T) {
	t.Run("start when disabled is a no-op", func(t *testing.T) {
		resetState(t)

		var calls int
		withFakeNewHost(t, func(_ ...libp2p.Option) (meshHost, error) {
			calls++
			return newFakeHost(), nil
		})

		if err := Start(context.Background(), Config{Enabled: false}); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if calls != 0 {
			t.Fatalf("newHost called %d times, want 0", calls)
		}
		if Enabled() {
			t.Fatal("expected Enabled() to be false")
		}
		if _, err := Status(); !errors.Is(err, errNotStarted) {
			t.Fatalf("Status: got %v, want errNotStarted", err)
		}
	})

	t.Run("start with enabled true but empty private key is a no-op", func(t *testing.T) {
		resetState(t)

		relayAddr, _ := newTestRelayAddr()

		var calls int
		withFakeNewHost(t, func(_ ...libp2p.Option) (meshHost, error) {
			calls++
			return newFakeHost(), nil
		})

		err := Start(context.Background(), Config{
			Enabled:    true,
			PrivateKey: "",
			RelayAddrs: []string{relayAddr},
		})
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if calls != 0 {
			t.Fatalf("newHost called %d times, want 0", calls)
		}
	})

	t.Run("start with enabled true but no relay addrs is a no-op", func(t *testing.T) {
		resetState(t)

		var calls int
		withFakeNewHost(t, func(_ ...libp2p.Option) (meshHost, error) {
			calls++
			return newFakeHost(), nil
		})

		err := Start(context.Background(), Config{
			Enabled:    true,
			PrivateKey: newTestPrivateKeyString(),
			RelayAddrs: nil,
		})
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if calls != 0 {
			t.Fatalf("newHost called %d times, want 0", calls)
		}
	})
}

func TestMeshnetStartValidation(t *testing.T) {
	t.Run("start with invalid private key returns an error without calling newHost", func(t *testing.T) {
		resetState(t)

		relayAddr, _ := newTestRelayAddr()

		var calls int
		withFakeNewHost(t, func(_ ...libp2p.Option) (meshHost, error) {
			calls++
			return newFakeHost(), nil
		})

		err := Start(context.Background(), Config{
			Enabled:    true,
			PrivateKey: "not-valid-base64!!!",
			RelayAddrs: []string{relayAddr},
		})
		if err == nil {
			t.Fatal("expected an error")
		}
		if calls != 0 {
			t.Fatalf("newHost called %d times, want 0", calls)
		}
	})

	t.Run("start with private key that is valid base64 but not a valid key returns an error", func(t *testing.T) {
		resetState(t)

		relayAddr, _ := newTestRelayAddr()

		var calls int
		withFakeNewHost(t, func(_ ...libp2p.Option) (meshHost, error) {
			calls++
			return newFakeHost(), nil
		})

		err := Start(context.Background(), Config{
			Enabled:    true,
			PrivateKey: crypto.ConfigEncodeKey([]byte("not a protobuf-serialized key")),
			RelayAddrs: []string{relayAddr},
		})
		if err == nil {
			t.Fatal("expected an error")
		}
		if calls != 0 {
			t.Fatalf("newHost called %d times, want 0", calls)
		}
	})

	t.Run("start with unparseable relay addr returns an error without calling newHost", func(t *testing.T) {
		resetState(t)

		var calls int
		withFakeNewHost(t, func(_ ...libp2p.Option) (meshHost, error) {
			calls++
			return newFakeHost(), nil
		})

		err := Start(context.Background(), Config{
			Enabled:    true,
			PrivateKey: newTestPrivateKeyString(),
			RelayAddrs: []string{"not a multiaddr"},
		})
		if err == nil {
			t.Fatal("expected an error")
		}
		if calls != 0 {
			t.Fatalf("newHost called %d times, want 0", calls)
		}
	})

	t.Run("start with relay addr missing a peer id returns an error without calling newHost", func(t *testing.T) {
		resetState(t)

		var calls int
		withFakeNewHost(t, func(_ ...libp2p.Option) (meshHost, error) {
			calls++
			return newFakeHost(), nil
		})

		err := Start(context.Background(), Config{
			Enabled:    true,
			PrivateKey: newTestPrivateKeyString(),
			RelayAddrs: []string{"/ip4/127.0.0.1/udp/4001/quic-v1"}, // no /p2p/<id>
		})
		if err == nil {
			t.Fatal("expected an error")
		}
		if calls != 0 {
			t.Fatalf("newHost called %d times, want 0", calls)
		}
	})

	t.Run("start surfaces a newHost construction failure", func(t *testing.T) {
		resetState(t)

		wantErr := errors.New("construct failed")
		withFakeNewHost(t, func(_ ...libp2p.Option) (meshHost, error) {
			return nil, wantErr
		})

		relayAddr, _ := newTestRelayAddr()
		err := Start(context.Background(), Config{
			Enabled:    true,
			PrivateKey: newTestPrivateKeyString(),
			RelayAddrs: []string{relayAddr},
		})
		if !strings.Contains(fmt.Sprint(err), "construct failed") {
			t.Fatalf("Start: got %v, want an error wrapping %v", err, wantErr)
		}
	})
}

func TestMeshnetStartIdempotency(t *testing.T) {
	t.Run("start twice returns the same result", func(t *testing.T) {
		resetState(t)

		fh := newFakeHost()
		var calls int
		withFakeNewHost(t, func(_ ...libp2p.Option) (meshHost, error) {
			calls++
			return fh, nil
		})

		relayAddr, _ := newTestRelayAddr()
		cfg := Config{Enabled: true, PrivateKey: newTestPrivateKeyString(), RelayAddrs: []string{relayAddr}}

		if err := Start(context.Background(), cfg); err != nil {
			t.Fatalf("first Start: %v", err)
		}
		if calls != 1 {
			t.Fatalf("newHost called %d times after first Start, want 1", calls)
		}

		// Second call, deliberately with a different (even invalid) Config —
		// idempotency must ignore it entirely.
		if err := Start(context.Background(), Config{Enabled: false}); err != nil {
			t.Fatalf("second Start: %v", err)
		}
		if calls != 1 {
			t.Fatalf("newHost called %d times after second Start, want still 1", calls)
		}
		if !Enabled() {
			t.Fatal("expected Enabled() to remain true from the first call")
		}
	})

	t.Run("concurrent start calls only initialize once", func(t *testing.T) {
		resetState(t)

		fh := newFakeHost()
		var mu sync.Mutex
		var calls int
		withFakeNewHost(t, func(_ ...libp2p.Option) (meshHost, error) {
			mu.Lock()
			calls++
			mu.Unlock()
			return fh, nil
		})

		relayAddr, _ := newTestRelayAddr()
		cfg := Config{Enabled: true, PrivateKey: newTestPrivateKeyString(), RelayAddrs: []string{relayAddr}}

		const n = 20
		var wg sync.WaitGroup
		errs := make([]error, n)
		wg.Add(n)
		for i := 0; i < n; i++ {
			go func(i int) {
				defer wg.Done()
				errs[i] = Start(context.Background(), cfg)
			}(i)
		}
		wg.Wait()

		for i, err := range errs {
			if err != nil {
				t.Fatalf("goroutine %d: Start returned %v, want nil", i, err)
			}
		}
		if calls != 1 {
			t.Fatalf("newHost called %d times, want exactly 1", calls)
		}
	})

	t.Run("a relay connect failure during start is non-fatal: start still succeeds", func(t *testing.T) {
		// This subtest replaces a prior version of this test (from before
		// Finding 2 of the final whole-branch mesh review) that asserted
		// the opposite: that a relay Connect failure during Start tore the
		// host down and made Start itself fail. That behavior meant a
		// single transiently-unreachable relay at process-start permanently
		// disabled mesh for the rest of the process's life, because Start's
		// result is latched (see the idempotency contract on Start) — no
		// retry, no recovery short of restarting the whole process. Since
		// go-libp2p's own AutoRelay subsystem (configured via
		// EnableAutoRelayWithStaticRelays, unconditionally, above this
		// loop) already retries connecting to these exact static relays in
		// the background on its own schedule regardless of this explicit
		// warm-up Connect's outcome, that Connect's failure is deliberately
		// no longer treated as fatal. This subtest proves the new,
		// resilient behavior; it deliberately does not restore the old
		// assertions (that would be re-introducing the bug Finding 2 fixes).
		resetState(t)

		fh := newFakeHost()
		wantErr := errors.New("boom: relay unreachable")
		fh.connectErr = wantErr

		withFakeNewHost(t, func(_ ...libp2p.Option) (meshHost, error) {
			return fh, nil
		})

		relayAddr, _ := newTestRelayAddr()
		cfg := Config{Enabled: true, PrivateKey: newTestPrivateKeyString(), RelayAddrs: []string{relayAddr}}

		err := Start(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Start: got %v, want nil (a relay Connect failure must not fail Start)", err)
		}
		if fh.closeCalls != 0 {
			t.Fatalf("host Close called %d times, want 0 (host must not be torn down over a relay warm-up connect failure)", fh.closeCalls)
		}

		// The singleton must consider itself started despite the relay
		// connect failure: Status, DialPeer, Listener must all work against
		// the constructed host (AutoRelay is expected to establish the
		// actual relay connection later, in the background).
		if _, err := Status(); err != nil {
			t.Fatalf("Status after Start with a failed relay warm-up connect: got %v, want nil", err)
		}
		if Enabled() != true {
			t.Fatal("expected Enabled() to be true")
		}

		// A second Start call must still just replay the cached (nil) result,
		// not attempt to reconnect or reconstruct the host.
		if err2 := Start(context.Background(), cfg); err2 != nil {
			t.Fatalf("second Start: got %v, want nil (cached)", err2)
		}
		if fh.closeCalls != 0 {
			t.Fatalf("host Close called %d times after retry, want still 0", fh.closeCalls)
		}
	})

	t.Run("newHost construction failure remains fatal", func(t *testing.T) {
		// Unlike the relay Connect warm-up above, a newHost (libp2p.New)
		// construction failure is an unconditional problem — no amount of
		// AutoRelay backoff fixes a host that never came up — so it must
		// remain fatal. Covered primarily by
		// "start surfaces a newHost construction failure" in
		// TestMeshnetStartValidation; this subtest exists alongside the
		// relay-connect-is-non-fatal case above purely so the two contrasting
		// outcomes (relay connect failure vs. host construction failure) are
		// easy to find next to each other and their fatal/non-fatal status
		// is explicit.
		resetState(t)

		wantErr := errors.New("construct failed")
		withFakeNewHost(t, func(_ ...libp2p.Option) (meshHost, error) {
			return nil, wantErr
		})

		relayAddr, _ := newTestRelayAddr()
		cfg := Config{Enabled: true, PrivateKey: newTestPrivateKeyString(), RelayAddrs: []string{relayAddr}}

		err := Start(context.Background(), cfg)
		if err == nil {
			t.Fatal("expected an error: newHost construction failure must remain fatal")
		}
		if !strings.Contains(err.Error(), "construct failed") {
			t.Fatalf("error %q does not wrap the underlying construct error", err)
		}
	})
}

func TestMeshnetStop(t *testing.T) {
	t.Run("stop resets state so a subsequent start calls newHost again", func(t *testing.T) {
		resetState(t)

		var calls int
		withFakeNewHost(t, func(_ ...libp2p.Option) (meshHost, error) {
			calls++
			return newFakeHost(), nil
		})

		relayAddr, _ := newTestRelayAddr()
		cfg := Config{Enabled: true, PrivateKey: newTestPrivateKeyString(), RelayAddrs: []string{relayAddr}}

		if err := Start(context.Background(), cfg); err != nil {
			t.Fatalf("first Start: %v", err)
		}
		if calls != 1 {
			t.Fatalf("newHost called %d times, want 1", calls)
		}

		if err := Stop(context.Background()); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		if Enabled() {
			t.Fatal("expected Enabled() to be false after Stop")
		}
		if _, err := Status(); !errors.Is(err, errNotStarted) {
			t.Fatalf("Status after Stop: got %v, want errNotStarted", err)
		}

		if err := Start(context.Background(), cfg); err != nil {
			t.Fatalf("second Start (post-Stop): %v", err)
		}
		if calls != 2 {
			t.Fatalf("newHost called %d times after Stop+Start, want 2 (proves genuine re-init)", calls)
		}
	})

	t.Run("stop closes the host and reports its close error", func(t *testing.T) {
		resetState(t)

		fh := newFakeHost()
		wantCloseErr := errors.New("close failed")
		withFakeNewHost(t, func(_ ...libp2p.Option) (meshHost, error) {
			return fh, nil
		})

		relayAddr, _ := newTestRelayAddr()
		cfg := Config{Enabled: true, PrivateKey: newTestPrivateKeyString(), RelayAddrs: []string{relayAddr}}
		if err := Start(context.Background(), cfg); err != nil {
			t.Fatalf("Start: %v", err)
		}

		fh.closeErr = wantCloseErr
		if err := Stop(context.Background()); !errors.Is(err, wantCloseErr) {
			t.Fatalf("Stop: got %v, want %v", err, wantCloseErr)
		}
	})

	t.Run("stop when never enabled returns errDisabled", func(t *testing.T) {
		resetState(t)

		if err := Stop(context.Background()); !errors.Is(err, errDisabled) {
			t.Fatalf("Stop: got %v, want errDisabled", err)
		}
	})
}

func TestMeshnetDialPeer(t *testing.T) {
	t.Run("dial peer resolves parses connects and opens a stream", func(t *testing.T) {
		resetState(t)

		fh := newFakeHost()
		withFakeNewHost(t, func(_ ...libp2p.Option) (meshHost, error) {
			return fh, nil
		})

		relayAddr, _ := newTestRelayAddr()
		cfg := Config{Enabled: true, PrivateKey: newTestPrivateKeyString(), RelayAddrs: []string{relayAddr}}
		if err := Start(context.Background(), cfg); err != nil {
			t.Fatalf("Start: %v", err)
		}

		targetID := newTestPeerID()
		fs := newFakeStream(targetID, "hello from peer")
		fh.newStreamRet = fs

		conn, err := DialPeer(context.Background(), "/ip4/127.0.0.1/udp/4002/quic-v1/p2p/"+targetID.String())
		if err != nil {
			t.Fatalf("DialPeer: %v", err)
		}
		defer conn.Close()

		if fh.connectCalls != 2 { // 1 relay connect during Start + 1 dial-time connect
			t.Fatalf("host.Connect called %d times, want 2", fh.connectCalls)
		}
		if fh.newStreamCalls != 1 {
			t.Fatalf("host.NewStream called %d times, want 1", fh.newStreamCalls)
		}

		if got := conn.LocalAddr().String(); got != fh.id.String() {
			t.Fatalf("LocalAddr = %q, want %q", got, fh.id.String())
		}
		if got := conn.RemoteAddr().String(); got != targetID.String() {
			t.Fatalf("RemoteAddr = %q, want %q", got, targetID.String())
		}
		if got := conn.LocalAddr().Network(); got != "libp2p" {
			t.Fatalf("LocalAddr.Network() = %q, want libp2p", got)
		}

		buf := make([]byte, len("hello from peer"))
		if _, err := io.ReadFull(conn, buf); err != nil {
			t.Fatalf("read: %v", err)
		}
		if string(buf) != "hello from peer" {
			t.Fatalf("read %q, want %q", buf, "hello from peer")
		}

		if _, err := conn.Write([]byte("hi back")); err != nil {
			t.Fatalf("write: %v", err)
		}
		if fs.String() != "hi back" {
			t.Fatalf("stream buffer = %q, want %q", fs.String(), "hi back")
		}

		if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatalf("SetDeadline: %v", err)
		}
	})

	t.Run("dial peer through a circuit relay address parses the target id", func(t *testing.T) {
		resetState(t)

		fh := newFakeHost()
		withFakeNewHost(t, func(_ ...libp2p.Option) (meshHost, error) {
			return fh, nil
		})

		relayAddr, relayID := newTestRelayAddr()
		cfg := Config{Enabled: true, PrivateKey: newTestPrivateKeyString(), RelayAddrs: []string{relayAddr}}
		if err := Start(context.Background(), cfg); err != nil {
			t.Fatalf("Start: %v", err)
		}

		targetID := newTestPeerID()
		fh.newStreamRet = newFakeStream(targetID, "")

		circuitAddr := fmt.Sprintf("/p2p/%s/p2p-circuit/p2p/%s", relayID.String(), targetID.String())
		conn, err := DialPeer(context.Background(), circuitAddr)
		if err != nil {
			t.Fatalf("DialPeer: %v", err)
		}
		defer conn.Close()

		if got := conn.RemoteAddr().String(); got != targetID.String() {
			t.Fatalf("RemoteAddr = %q, want %q (circuit target)", got, targetID.String())
		}
	})

	t.Run("dial peer with an unparseable address returns an error", func(t *testing.T) {
		resetState(t)

		fh := newFakeHost()
		withFakeNewHost(t, func(_ ...libp2p.Option) (meshHost, error) {
			return fh, nil
		})
		relayAddr, _ := newTestRelayAddr()
		if err := Start(context.Background(), Config{Enabled: true, PrivateKey: newTestPrivateKeyString(), RelayAddrs: []string{relayAddr}}); err != nil {
			t.Fatalf("Start: %v", err)
		}

		if _, err := DialPeer(context.Background(), "not a multiaddr"); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("dial peer with an address missing a peer id returns an error", func(t *testing.T) {
		resetState(t)

		fh := newFakeHost()
		withFakeNewHost(t, func(_ ...libp2p.Option) (meshHost, error) {
			return fh, nil
		})
		relayAddr, _ := newTestRelayAddr()
		if err := Start(context.Background(), Config{Enabled: true, PrivateKey: newTestPrivateKeyString(), RelayAddrs: []string{relayAddr}}); err != nil {
			t.Fatalf("Start: %v", err)
		}

		if _, err := DialPeer(context.Background(), "/ip4/127.0.0.1/udp/1/quic-v1"); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("dial peer surfaces a connect failure", func(t *testing.T) {
		resetState(t)

		fh := newFakeHost()
		withFakeNewHost(t, func(_ ...libp2p.Option) (meshHost, error) {
			return fh, nil
		})
		relayAddr, _ := newTestRelayAddr()
		if err := Start(context.Background(), Config{Enabled: true, PrivateKey: newTestPrivateKeyString(), RelayAddrs: []string{relayAddr}}); err != nil {
			t.Fatalf("Start: %v", err)
		}

		fh.connectErr = errors.New("dial refused")
		if _, err := DialPeer(context.Background(), "/ip4/127.0.0.1/udp/1/quic-v1/p2p/"+newTestPeerID().String()); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("dial peer surfaces a new stream failure", func(t *testing.T) {
		resetState(t)

		fh := newFakeHost()
		withFakeNewHost(t, func(_ ...libp2p.Option) (meshHost, error) {
			return fh, nil
		})
		relayAddr, _ := newTestRelayAddr()
		if err := Start(context.Background(), Config{Enabled: true, PrivateKey: newTestPrivateKeyString(), RelayAddrs: []string{relayAddr}}); err != nil {
			t.Fatalf("Start: %v", err)
		}

		fh.newStreamErr = errors.New("stream refused")
		if _, err := DialPeer(context.Background(), "/ip4/127.0.0.1/udp/1/quic-v1/p2p/"+newTestPeerID().String()); err == nil {
			t.Fatal("expected an error")
		}
	})
}

func TestMeshnetListener(t *testing.T) {
	t.Run("listener accept returns a stream pushed by the registered handler", func(t *testing.T) {
		resetState(t)

		fh := newFakeHost()
		withFakeNewHost(t, func(_ ...libp2p.Option) (meshHost, error) {
			return fh, nil
		})
		relayAddr, _ := newTestRelayAddr()
		if err := Start(context.Background(), Config{Enabled: true, PrivateKey: newTestPrivateKeyString(), RelayAddrs: []string{relayAddr}}); err != nil {
			t.Fatalf("Start: %v", err)
		}

		l, err := Listener()
		if err != nil {
			t.Fatalf("Listener: %v", err)
		}
		if got := l.Addr().String(); got != fh.id.String() {
			t.Fatalf("Addr() = %q, want %q", got, fh.id.String())
		}

		remoteID := newTestPeerID()
		fs := newFakeStream(remoteID, "incoming")

		acceptErrCh := make(chan error, 1)
		connCh := make(chan net.Conn, 1)
		go func() {
			c, err := l.Accept()
			if err != nil {
				acceptErrCh <- err
				return
			}
			connCh <- c
			acceptErrCh <- nil
		}()

		fh.callHandler(fs)

		if err := <-acceptErrCh; err != nil {
			t.Fatalf("Accept: %v", err)
		}
		conn := <-connCh
		defer conn.Close()

		if got := conn.RemoteAddr().String(); got != remoteID.String() {
			t.Fatalf("RemoteAddr = %q, want %q", got, remoteID.String())
		}

		buf := make([]byte, len("incoming"))
		if _, err := io.ReadFull(conn, buf); err != nil {
			t.Fatalf("read: %v", err)
		}
		if string(buf) != "incoming" {
			t.Fatalf("read %q, want %q", buf, "incoming")
		}
	})

	t.Run("listener close unblocks a pending accept", func(t *testing.T) {
		resetState(t)

		fh := newFakeHost()
		withFakeNewHost(t, func(_ ...libp2p.Option) (meshHost, error) {
			return fh, nil
		})
		relayAddr, _ := newTestRelayAddr()
		if err := Start(context.Background(), Config{Enabled: true, PrivateKey: newTestPrivateKeyString(), RelayAddrs: []string{relayAddr}}); err != nil {
			t.Fatalf("Start: %v", err)
		}

		l, err := Listener()
		if err != nil {
			t.Fatalf("Listener: %v", err)
		}

		acceptErrCh := make(chan error, 1)
		go func() {
			_, err := l.Accept()
			acceptErrCh <- err
		}()

		if err := l.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}

		if err := <-acceptErrCh; !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Accept after Close: got %v, want net.ErrClosed", err)
		}

		// A handler callback arriving after Close must reset the stream
		// rather than block forever.
		fs := newFakeStream(newTestPeerID(), "")
		fh.callHandler(fs)
		if fs.resetCalls != 1 {
			t.Fatalf("stream Reset called %d times after listener close, want 1", fs.resetCalls)
		}
	})
}

func TestMeshnetStatus(t *testing.T) {
	t.Run("status reports peer id and connected relays", func(t *testing.T) {
		resetState(t)

		fh := newFakeHost()
		withFakeNewHost(t, func(_ ...libp2p.Option) (meshHost, error) {
			return fh, nil
		})
		relayAddr, relayID := newTestRelayAddr()
		if err := Start(context.Background(), Config{Enabled: true, PrivateKey: newTestPrivateKeyString(), RelayAddrs: []string{relayAddr}}); err != nil {
			t.Fatalf("Start: %v", err)
		}

		status, err := Status()
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if status.PeerID != fh.id.String() {
			t.Fatalf("PeerID = %q, want %q", status.PeerID, fh.id.String())
		}
		if status.Connected {
			t.Fatal("expected Connected false before the relay reports connectedness")
		}

		fh.network.setConnectedness(relayID, network.Connected)

		status, err = Status()
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if !status.Connected {
			t.Fatal("expected Connected true once the relay reports connectedness")
		}
		if len(status.Relays) != 1 {
			t.Fatalf("Relays = %v, want exactly one entry", status.Relays)
		}
	})

	t.Run("status is best effort when the host reports no network", func(t *testing.T) {
		resetState(t)

		fh := newFakeHost()
		fh.network = nil
		withFakeNewHost(t, func(_ ...libp2p.Option) (meshHost, error) {
			return fh, nil
		})
		relayAddr, _ := newTestRelayAddr()
		if err := Start(context.Background(), Config{Enabled: true, PrivateKey: newTestPrivateKeyString(), RelayAddrs: []string{relayAddr}}); err != nil {
			t.Fatalf("Start: %v", err)
		}

		status, err := Status()
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if status.Connected {
			t.Fatal("expected Connected false when Network() is nil")
		}
		if len(status.Relays) != 0 {
			t.Fatalf("Relays = %v, want none", status.Relays)
		}
	})
}

func TestMeshnetListenMeshOption(t *testing.T) {
	t.Run("listen mesh true also configures the relay service without changing the seam", func(t *testing.T) {
		resetState(t)

		var gotOptsLen int
		withFakeNewHost(t, func(opts ...libp2p.Option) (meshHost, error) {
			gotOptsLen = len(opts)
			return newFakeHost(), nil
		})

		relayAddr, _ := newTestRelayAddr()
		cfgListenMeshOff := Config{Enabled: true, PrivateKey: newTestPrivateKeyString(), RelayAddrs: []string{relayAddr}, ListenMesh: false}
		if err := Start(context.Background(), cfgListenMeshOff); err != nil {
			t.Fatalf("Start: %v", err)
		}
		optsWithoutRelayService := gotOptsLen

		resetState(t)
		withFakeNewHost(t, func(opts ...libp2p.Option) (meshHost, error) {
			gotOptsLen = len(opts)
			return newFakeHost(), nil
		})
		cfgListenMeshOn := Config{Enabled: true, PrivateKey: newTestPrivateKeyString(), RelayAddrs: []string{relayAddr}, ListenMesh: true}
		if err := Start(context.Background(), cfgListenMeshOn); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if gotOptsLen != optsWithoutRelayService+1 {
			t.Fatalf("ListenMesh:true produced %d libp2p.Options, want %d (one more than ListenMesh:false's %d)", gotOptsLen, optsWithoutRelayService+1, optsWithoutRelayService)
		}
	})
}
