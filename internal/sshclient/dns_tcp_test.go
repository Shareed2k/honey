package sshclient

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func frameDNS(msg []byte) []byte {
	out := make([]byte, 2+len(msg))
	binary.BigEndian.PutUint16(out[:2], uint16(len(msg)))
	copy(out[2:], msg)
	return out
}

func TestDNSStreamConn_Write(t *testing.T) {
	client, peer := net.Pipe()
	defer client.Close()
	defer peer.Close()
	dc := newDNSStreamConn(client)

	payload := []byte("hello-dns-query")
	errc := make(chan error, 1)
	go func() {
		_, e := dc.Write(payload)
		errc <- e
	}()

	hdr := make([]byte, 2)
	require.NoError(t, readFullDeadline(t, peer, hdr))
	assert.Equal(t, uint16(len(payload)), binary.BigEndian.Uint16(hdr))

	body := make([]byte, len(payload))
	require.NoError(t, readFullDeadline(t, peer, body))
	assert.Equal(t, payload, body)
	require.NoError(t, <-errc)
}

func TestDNSStreamConn_Read(t *testing.T) {
	t.Run("full message", func(t *testing.T) {
		client, peer := net.Pipe()
		defer client.Close()
		defer peer.Close()
		dc := newDNSStreamConn(client)

		resp := []byte("a-dns-response")
		go func() { _, _ = peer.Write(frameDNS(resp)) }()

		buf := make([]byte, 512)
		n, err := dc.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, resp, buf[:n])
	})

	t.Run("buffer smaller than message drains remainder", func(t *testing.T) {
		client, peer := net.Pipe()
		defer client.Close()
		defer peer.Close()
		dc := newDNSStreamConn(client)

		resp := []byte("0123456789") // 10 bytes
		go func() { _, _ = peer.Write(frameDNS(resp)) }()

		got := make([]byte, 0, 10)
		for _, want := range []int{4, 4, 2} {
			b := make([]byte, 4)
			n, err := dc.Read(b)
			require.NoError(t, err)
			assert.Equal(t, want, n)
			got = append(got, b[:n]...)
		}
		assert.Equal(t, resp, got)
	})

	t.Run("two datagrams", func(t *testing.T) {
		client, peer := net.Pipe()
		defer client.Close()
		defer peer.Close()
		dc := newDNSStreamConn(client)

		r1, r2 := []byte("first"), []byte("second-answer")
		go func() {
			_, _ = peer.Write(frameDNS(r1))
			_, _ = peer.Write(frameDNS(r2))
		}()

		buf := make([]byte, 512)
		n, err := dc.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, r1, buf[:n])

		n, err = dc.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, r2, buf[:n])
	})
}

func TestIsDNSPort(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"1.1.1.1:53", true},
		{"8.8.8.8:53", true},
		{"[2001:4860:4860::8888]:53", true},
		{"8.8.8.8:853", false},
		{"10.0.0.1:443", false},
		{"not-a-host-port", false},
		{"1.1.1.1", false},
	}
	for _, tt := range tests {
		assert.Equalf(t, tt.want, isDNSPort(tt.addr), "isDNSPort(%q)", tt.addr)
	}
}

// fakeDNSDialer is an SSHDialer whose TCP dials are answered by an in-process
// DNS-over-TCP responder, standing in for an SSH channel to a real resolver.
type fakeDNSDialer struct {
	t      *testing.T
	answer []byte
}

func (f *fakeDNSDialer) Dial(network, addr string) (net.Conn, error) {
	if network != "tcp" {
		return nil, fmt.Errorf("fakeDNSDialer: expected tcp, got %q for %s", network, addr)
	}
	serverSide, clientSide := net.Pipe()
	go func() {
		defer serverSide.Close()
		var hdr [2]byte
		if _, err := io.ReadFull(serverSide, hdr[:]); err != nil {
			return
		}
		q := make([]byte, binary.BigEndian.Uint16(hdr[:]))
		if _, err := io.ReadFull(serverSide, q); err != nil {
			return
		}
		_, _ = serverSide.Write(frameDNS(f.answer))
	}()
	return clientSide, nil
}

// TestAssociate_DNSOverTCP drives the real go-socks5 server (via
// StartDynamicForwardMulti) through a SOCKS5 UDP ASSOCIATE and asserts a DNS
// datagram round-trips when the underlying transport is TCP-only.
func TestAssociate_DNSOverTCP(t *testing.T) {
	answer := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	host, port, stop, err := StartDynamicForwardMulti(
		ctx,
		[]WeightedClient{{Client: &fakeDNSDialer{t: t, answer: answer}, Weight: 1}},
		"127.0.0.1", 0,
	)
	require.NoError(t, err)
	defer stop()

	got := socks5UDPQuery(t, net.JoinHostPort(host, strconv.Itoa(port)),
		net.ParseIP("1.1.1.1"), 53, []byte("dns-query"))
	assert.Equal(t, answer, got)
}

// socks5UDPQuery performs a minimal SOCKS5 UDP ASSOCIATE and sends one datagram
// to dstIP:dstPort, returning the response payload (SOCKS UDP header stripped).
func socks5UDPQuery(t *testing.T, socksAddr string, dstIP net.IP, dstPort int, payload []byte) []byte {
	t.Helper()
	ctrl, err := net.DialTimeout("tcp", socksAddr, 2*time.Second)
	require.NoError(t, err)
	defer ctrl.Close()
	require.NoError(t, ctrl.SetDeadline(time.Now().Add(3*time.Second)))

	// Greeting: VER=5, 1 method, NO-AUTH.
	_, err = ctrl.Write([]byte{0x05, 0x01, 0x00})
	require.NoError(t, err)
	mrep := make([]byte, 2)
	require.NoError(t, readFullDeadline(t, ctrl, mrep))
	require.Equal(t, []byte{0x05, 0x00}, mrep)

	// UDP ASSOCIATE with DST 0.0.0.0:0.
	_, err = ctrl.Write([]byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	require.NoError(t, err)

	head := make([]byte, 4)
	require.NoError(t, readFullDeadline(t, ctrl, head))
	require.Equalf(t, byte(0x00), head[1], "associate reply status")
	var bndIP net.IP
	switch head[3] {
	case 0x01:
		b := make([]byte, 4)
		require.NoError(t, readFullDeadline(t, ctrl, b))
		bndIP = net.IP(b)
	case 0x04:
		b := make([]byte, 16)
		require.NoError(t, readFullDeadline(t, ctrl, b))
		bndIP = net.IP(b)
	default:
		t.Fatalf("unexpected ATYP %d", head[3])
	}
	pb := make([]byte, 2)
	require.NoError(t, readFullDeadline(t, ctrl, pb))
	bndPort := int(binary.BigEndian.Uint16(pb))
	if bndIP.IsUnspecified() {
		bndIP = net.ParseIP("127.0.0.1")
	}

	uc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	require.NoError(t, err)
	defer uc.Close()
	require.NoError(t, uc.SetDeadline(time.Now().Add(3*time.Second)))

	// SOCKS UDP datagram: RSV(2) FRAG(1) ATYP(1) ADDR(4) PORT(2) DATA.
	dst4 := dstIP.To4()
	dg := make([]byte, 0, 4+len(dst4)+2+len(payload))
	dg = append(dg, 0x00, 0x00, 0x00, 0x01)
	dg = append(dg, dst4...)
	portb := make([]byte, 2)
	binary.BigEndian.PutUint16(portb, uint16(dstPort))
	dg = append(dg, portb...)
	dg = append(dg, payload...)
	_, err = uc.WriteToUDP(dg, &net.UDPAddr{IP: bndIP, Port: bndPort})
	require.NoError(t, err)

	buf := make([]byte, 65535)
	n, _, err := uc.ReadFromUDP(buf)
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, 10, "datagram shorter than SOCKS UDP header")
	return append([]byte(nil), buf[10:n]...)
}

func readFullDeadline(t *testing.T, c net.Conn, buf []byte) error {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, err := io.ReadFull(c, buf)
	return err
}
