// Package udprelaywire is the shared datagram-frame codec and target
// validator for the UDP relay bridge between honeyprovider (client) and
// webserver (server). It is a leaf package with no honey dependencies so
// both sides can import it without creating an import cycle.
package udprelaywire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
)

// maxPayloadSize is the largest UDP datagram payload a frame may carry: the
// maximum theoretical UDP payload size (65535 byte IP payload minus the
// 8-byte UDP header minus the minimum 20-byte IP header). It is the hard
// upper bound for a UDP datagram.
const maxPayloadSize = 65507

// WriteFrame writes payload to w as a single length-prefixed frame:
// a big-endian uint16 length header followed by the raw payload bytes.
// It returns an error if payload is larger than a UDP datagram can carry
// (65507 bytes) since the length would not fit the uint16 header faithfully
// as a real datagram size.
func WriteFrame(w io.Writer, payload []byte) error {
	n := len(payload)
	if n > maxPayloadSize {
		return fmt.Errorf("udprelaywire: payload too large: %d bytes (max %d)", n, maxPayloadSize)
	}

	// Encode n as a big-endian 2-byte length header (matching
	// encoding/binary.BigEndian's byte order) using explicit bitmasks
	// rather than a uint16(n) conversion: masking each byte with & 0xff
	// bounds its range in the expression itself, which is provably safe
	// independent of the length check above (no wide-to-narrow integer
	// conversion for a static analyzer to flag).
	header := [2]byte{
		byte((n >> 8) & 0xff),
		byte(n & 0xff),
	}

	if _, err := w.Write(header[:]); err != nil {
		return fmt.Errorf("udprelaywire: write frame header: %w", err)
	}
	if n > 0 {
		if _, err := w.Write(payload); err != nil {
			return fmt.Errorf("udprelaywire: write frame payload: %w", err)
		}
	}
	return nil
}

// ReadFrame reads one length-prefixed frame from r, as written by
// WriteFrame, and returns its payload. If r is exhausted before any header
// bytes are read, it returns io.EOF unwrapped (via io.ReadFull) so callers
// can detect a clean stream end with errors.Is(err, io.EOF). A partial
// header, or a body cut off after a complete header was read (including a
// truncation of zero body bytes), is reported as io.ErrUnexpectedEOF, per
// io.ReadFull's contract, so callers never mistake a truncated frame for a
// clean stream close.
func ReadFrame(r io.Reader) ([]byte, error) {
	var header [2]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}

	n := binary.BigEndian.Uint16(header[:])
	if n > maxPayloadSize {
		return nil, fmt.Errorf("udprelaywire: frame length %d exceeds max %d", n, maxPayloadSize)
	}

	payload := make([]byte, n)
	if n > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			if errors.Is(err, io.EOF) {
				// A complete header was already read, so running out of
				// bytes now means the stream was truncated mid-frame, not
				// a clean end. Promote to io.ErrUnexpectedEOF so callers
				// looping on errors.Is(err, io.EOF) don't mistake this for
				// a clean stream close.
				err = io.ErrUnexpectedEOF
			}
			return nil, fmt.Errorf("udprelaywire: read frame payload: %w", err)
		}
	}
	return payload, nil
}

// hostnamePattern matches a syntactically plain DNS hostname: letters,
// digits, '.' and '-' only. It is deliberately permissive about label
// structure (unlike a strict RFC 1123 hostname regexp) but anchored on
// both ends so it rejects any shell metacharacter (spaces, ;, |, &, $,
// backticks, etc.) that could be interpolated into a remote shell command.
var hostnamePattern = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)

// maxHostnameLength is the maximum length of a DNS hostname per RFC 1035.
const maxHostnameLength = 253

// ValidateTarget validates hostport as a safe UDP relay dial target: it
// must split into a host and port via net.SplitHostPort, the port must be
// in the valid 1..65535 range (0 or unparseable rejected), and the host
// must be either a valid IP literal (net.ParseIP) or a DNS hostname
// matching hostnamePattern with length <= 253. This rejects shell
// metacharacters and other unsafe input before hostport is used to dial or
// interpolated into any command.
func ValidateTarget(hostport string) error {
	host, portStr, err := net.SplitHostPort(hostport)
	if err != nil {
		return fmt.Errorf("udprelaywire: invalid target %q: %w", hostport, err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("udprelaywire: invalid target %q: invalid port %q", hostport, portStr)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("udprelaywire: invalid target %q: port %d out of range (1..65535)", hostport, port)
	}

	if net.ParseIP(host) != nil {
		return nil
	}
	if len(host) > maxHostnameLength || !hostnamePattern.MatchString(host) {
		return fmt.Errorf("udprelaywire: invalid target %q: host %q must be an IP address or a DNS hostname (letters, digits, '.', '-' only)", hostport, host)
	}
	return nil
}
