package sshclient

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
)

// SSH direct-tcpip channels carry only TCP, so UDP datagrams (DNS) cannot be
// forwarded directly. DNS is the one universal exception: every resolver also
// serves TCP:53 using RFC 7766 framing (a 2-byte big-endian length prefix
// followed by the message). dnsStreamConn adapts a TCP (SSH-backed) connection
// to the datagram-oriented net.Conn that go-socks5's UDP ASSOCIATE relay
// expects: one Write per query, one Read per response.
type dnsStreamConn struct {
	net.Conn
	readBuf []byte // leftover from a response larger than the caller's buffer
}

// newDNSStreamConn wraps an SSH-backed TCP conn with DNS-over-TCP framing.
func newDNSStreamConn(c net.Conn) net.Conn {
	return &dnsStreamConn{Conn: c}
}

// Write frames one DNS query as [uint16 length][message] in a single write so
// the length prefix and body are never split across SSH packets.
func (c *dnsStreamConn) Write(p []byte) (int, error) {
	if len(p) > math.MaxUint16 {
		return 0, fmt.Errorf("dns: payload too large (%d bytes)", len(p))
	}
	// Explicitly cast to uint16 after bounds check
	l := uint16(len(p)) // #nosec G115 -- bounds checked above
	frame := make([]byte, 2+len(p))
	binary.BigEndian.PutUint16(frame[:2], l)
	copy(frame[2:], p)
	if _, err := c.Conn.Write(frame); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Read returns exactly one DNS response (the message with its length prefix
// stripped). If p is smaller than the message the remainder is buffered and
// drained on subsequent reads.
func (c *dnsStreamConn) Read(p []byte) (int, error) {
	if len(c.readBuf) > 0 {
		n := copy(p, c.readBuf)
		c.readBuf = c.readBuf[n:]
		return n, nil
	}

	var hdr [2]byte
	if _, err := io.ReadFull(c.Conn, hdr[:]); err != nil {
		return 0, err
	}
	msgLen := binary.BigEndian.Uint16(hdr[:])
	msg := make([]byte, msgLen)
	if _, err := io.ReadFull(c.Conn, msg); err != nil {
		return 0, err
	}

	n := copy(p, msg)
	if n < len(msg) {
		c.readBuf = msg[n:]
	}
	return n, nil
}

// isDNSPort reports whether hostport targets the DNS port (53).
func isDNSPort(hostport string) bool {
	_, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return false
	}
	return port == "53"
}
