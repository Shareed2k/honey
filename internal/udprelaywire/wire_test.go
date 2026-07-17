package udprelaywire

import (
	"bytes"
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
	_, err := ReadFrame(bytes.NewReader(nil))
	require.ErrorIs(t, err, io.EOF)
}

func TestValidateTarget(t *testing.T) {
	require.NoError(t, ValidateTarget("10.0.0.5:53"))
	require.NoError(t, ValidateTarget("dns.internal:53"))
	require.Error(t, ValidateTarget("bad;host:53"))
	require.Error(t, ValidateTarget("10.0.0.5:0"))
	require.Error(t, ValidateTarget("nohostport"))
}
