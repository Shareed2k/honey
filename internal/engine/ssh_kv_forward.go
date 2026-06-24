package engine

import (
	"fmt"
	"io"
	"net"
	"time"

	"github.com/shareed2k/honey/internal/sshclient"
	"github.com/shareed2k/honey/internal/stepkv"
)

const stepKVTunnelTTL = 30 * time.Minute

// attachKVRemoteForwardToSession opens an SSH remote listen on the target, proxies accepted remote
// connections to the given stepkv session's local TCP address, and returns env vars plus a stop func that
// closes only the remote listener (not the stepkv session).
func attachKVRemoteForwardToSession(hc *sshclient.HoneyClient, sess *stepkv.Session) (map[string]string, func(), error) {
	if sess == nil {
		return nil, nil, fmt.Errorf("stepkv: nil session")
	}
	leaf := hc.LeafSSH()
	if leaf == nil {
		return nil, nil, fmt.Errorf("ssh: leaf client unavailable")
	}
	remLn, err := leaf.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, fmt.Errorf("ssh remote listen: %w", err)
	}
	localDial := sess.LocalTCPAddr()
	if localDial == "" {
		_ = remLn.Close()
		return nil, nil, fmt.Errorf("stepkv: empty local dial address")
	}
	remoteURL := "http://" + remLn.Addr().String()

	go func() {
		for {
			cRem, accErr := remLn.Accept()
			if accErr != nil {
				return
			}
			go proxyKVBridge(cRem, localDial)
		}
	}()

	stop := func() {
		_ = remLn.Close()
	}

	env := map[string]string{
		"HONEY_KV_URL":   remoteURL,
		"HONEY_KV_TOKEN": sess.Token(),
	}
	return env, stop, nil
}

// attachStepKVRemoteForward starts a loopback stepkv HTTP server, opens an SSH remote listen on the target,
// proxies accepted remote connections to that server, and returns env vars for the remote shell plus a stop func.
func attachStepKVRemoteForward(hc *sshclient.HoneyClient, ttl time.Duration) (map[string]string, func(), error) {
	if ttl <= 0 {
		ttl = stepKVTunnelTTL
	}
	sess, err := stepkv.Start(ttl)
	if err != nil {
		return nil, nil, err
	}
	env, stopForward, err := attachKVRemoteForwardToSession(hc, sess)
	if err != nil {
		_ = sess.Close()
		return nil, nil, err
	}
	stop := func() {
		stopForward()
		_ = sess.Close()
	}
	return env, stop, nil
}

func proxyKVBridge(cRem net.Conn, localAddr string) {
	defer func() { _ = cRem.Close() }()
	cLoc, err := net.DialTimeout("tcp", localAddr, 15*time.Second)
	if err != nil {
		return
	}
	defer func() { _ = cLoc.Close() }()
	go func() { _, _ = io.Copy(cLoc, cRem) }()
	_, _ = io.Copy(cRem, cLoc)
}
