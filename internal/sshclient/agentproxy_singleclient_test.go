package sshclient

import (
	"errors"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// poisonedConn fails every read and write. sshagent.New hands back an agent
// already bound to its connection; if withAgentConn builds a SECOND client over
// that same connection, the operation talks to this conn directly and fails
// (in production it did worse — two clients on one socket deadlocked macOS's
// launchd agent until the 20s op timeout, adding 20s to the first dial of every
// process).
type poisonedConn struct{ net.Conn }

func (poisonedConn) Read([]byte) (int, error) {
	return 0, errors.New("conn read: nothing may read this connection directly")
}

func (poisonedConn) Write([]byte) (int, error) {
	return 0, errors.New("conn write: nothing may write this connection directly")
}
func (poisonedConn) Close() error                     { return nil }
func (poisonedConn) SetDeadline(time.Time) error      { return nil }
func (poisonedConn) SetReadDeadline(time.Time) error  { return nil }
func (poisonedConn) SetWriteDeadline(time.Time) error { return nil }
func (poisonedConn) LocalAddr() net.Addr              { return nil }
func (poisonedConn) RemoteAddr() net.Addr             { return nil }

// stubAgent answers List without touching any connection.
type stubAgent struct{ agent.ExtendedAgent }

func (stubAgent) List() ([]*agent.Key, error) {
	return []*agent.Key{{Format: ssh.KeyAlgoED25519, Blob: []byte("blob"), Comment: "stub"}}, nil
}

func TestWithAgentConn_UsesTheAgentBoundToTheConnection(t *testing.T) {
	orig := newAgentConn
	t.Cleanup(func() { newAgentConn = orig })
	newAgentConn = func() (agent.Agent, net.Conn, error) { return stubAgent{}, poisonedConn{}, nil }

	var got []*agent.Key
	err := withAgentConn(func(a agent.ExtendedAgent) error {
		var e error
		got, e = a.List()
		return e
	})
	if err != nil {
		t.Fatalf("withAgentConn: %v (it must use the agent sshagent.New returned, not wrap its conn)", err)
	}
	if len(got) != 1 || got[0].Comment != "stub" {
		t.Fatalf("keys = %+v, want the stub agent's single key", got)
	}
}
