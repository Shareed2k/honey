package udprelaywire

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFrameRoundTrip(t *testing.T) {
	for _, n := range []int{0, 1, 1500, 65507} {
		var buf bytes.Buffer
		payload := bytes.Repeat([]byte{0xab}, n)
		require.NoError(t, WriteFrame(&buf, payload))
		got, err := ReadFrame(&buf)
		require.NoError(t, err)
		require.Equal(t, payload, got)
	}
}

func TestWriteFrame_TooLarge(t *testing.T) {
	require.Error(t, WriteFrame(io.Discard, make([]byte, 65508)))
}

func TestReadFrame_EOF(t *testing.T) {
	// Clean end of stream: zero bytes available when reading the header.
	_, err := ReadFrame(bytes.NewReader(nil))
	require.ErrorIs(t, err, io.EOF)
}

func TestReadFrame_TruncatedAfterHeader(t *testing.T) {
	// A complete 2-byte header claiming a non-zero payload (5 bytes), but
	// the stream ends right there with no body bytes at all. This must not
	// be reported as a clean io.EOF: a caller looping on
	// errors.Is(err, io.EOF) to detect stream close would otherwise
	// silently treat this truncated frame as a clean end.
	_, err := ReadFrame(bytes.NewReader([]byte{0x00, 0x05}))
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.False(t, errors.Is(err, io.EOF), "truncated frame must not report bare io.EOF")
}

func TestReadFrame_LengthExceedsMax(t *testing.T) {
	// A header declaring the max uint16 length (65535), which exceeds the
	// maxPayloadSize (65507) enforced on the write side, must be rejected
	// before allocating/reading the body.
	_, err := ReadFrame(bytes.NewReader([]byte{0xff, 0xff}))
	require.Error(t, err)
	require.False(t, errors.Is(err, io.EOF))
	require.False(t, errors.Is(err, io.ErrUnexpectedEOF))
}

func TestValidateTarget(t *testing.T) {
	require.NoError(t, ValidateTarget("10.0.0.5:53"))
	require.NoError(t, ValidateTarget("dns.internal:53"))
	require.Error(t, ValidateTarget("bad;host:53"))
	require.Error(t, ValidateTarget("10.0.0.5:0"))
	require.Error(t, ValidateTarget("nohostport"))
}
