package sshgateway

import "io"

// crlfWriter translates a bare LF (a '\n' not already preceded by '\r') into CRLF
// on the way to w, performing the LF->CRLF "cooking" a tty's line discipline
// would normally do.
//
// It is used for native-provider exec output (docker/k8s/mesh) when the client
// requested a PTY — `ssh -t <resource> <cmd>`. Those commands run through
// HostClient.RunWithStreams, which has no remote tty, so their LF-terminated
// lines would "staircase" in the client's raw-mode terminal (each line stepping
// right instead of returning to column 0). Plain SSH targets don't need this: the
// #169 path allocates a real remote tty. Interactive shells on native providers
// also don't need it — those run through a real provider tty.
//
// The last emitted byte is tracked across Write calls so a CRLF split over two
// writes ("…\r" then "\n…") is not double-cooked into "\r\r\n".
type crlfWriter struct {
	w    io.Writer
	prev byte // last byte processed in a previous Write (0 before the first byte)
}

func newCRLFWriter(w io.Writer) *crlfWriter { return &crlfWriter{w: w} }

func (c *crlfWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	out := make([]byte, 0, len(p)+8)
	prev := c.prev
	for _, b := range p {
		if b == '\n' && prev != '\r' {
			out = append(out, '\r', '\n')
		} else {
			out = append(out, b)
		}
		prev = b
	}
	if _, err := c.w.Write(out); err != nil {
		return 0, err
	}
	c.prev = prev
	return len(p), nil
}
