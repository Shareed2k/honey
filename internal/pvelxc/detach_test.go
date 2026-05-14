package pvelxc

import (
	"bytes"
	"testing"
)

func TestCtrlBracketDetachByte(t *testing.T) {
	t.Parallel()
	if CtrlBracketDetach != 0x1d {
		t.Fatalf("want 0x1d got %#x", CtrlBracketDetach)
	}
}

func TestStdinChunkDetachIndex(t *testing.T) {
	t.Parallel()
	p := []byte("ab" + string(CtrlBracketDetach) + "c")
	i := bytes.IndexByte(p, CtrlBracketDetach)
	if i != 2 {
		t.Fatalf("index %d", i)
	}
}
