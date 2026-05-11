package ui

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

func TestReadWriteHkvFrame_roundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := writeHkvFrame(&buf, nil, hkvFrameData, 7, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	typ, cid, pay, err := readHkvFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if typ != hkvFrameData || cid != 7 || string(pay) != "hello" {
		t.Fatalf("got typ=%d cid=%d pay=%q err=%v", typ, cid, pay, err)
	}
}

func TestReadWriteHkvFrame_emptyPayload(t *testing.T) {
	var buf bytes.Buffer
	if err := writeHkvFrame(&buf, nil, hkvFrameClose, 99, nil); err != nil {
		t.Fatal(err)
	}
	typ, cid, pay, err := readHkvFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if typ != hkvFrameClose || cid != 99 || len(pay) != 0 {
		t.Fatalf("got typ=%d cid=%d len(pay)=%d", typ, cid, len(pay))
	}
}

func TestReadHkvFrame_tooLarge(t *testing.T) {
	var buf bytes.Buffer
	var hdr [9]byte
	hdr[0] = hkvFrameData
	binary.BigEndian.PutUint32(hdr[1:5], 1)
	binary.BigEndian.PutUint32(hdr[5:9], hkvMaxFramePayload+1)
	_, _ = buf.Write(hdr[:])
	_, _, _, err := readHkvFrame(&buf)
	if err == nil {
		t.Fatal("expected error for oversized frame")
	}
}

func TestReadHkvFrame_EOF(t *testing.T) {
	r := bytes.NewReader(nil)
	_, _, _, err := readHkvFrame(r)
	if err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestReadHkvFrame_shortPayload(t *testing.T) {
	var hdr [9]byte
	hdr[0] = hkvFrameData
	binary.BigEndian.PutUint32(hdr[1:5], 3)
	binary.BigEndian.PutUint32(hdr[5:9], 10)
	r := bytes.NewReader(append(hdr[:], []byte{1, 2, 3}...))
	_, _, _, err := readHkvFrame(r)
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("expected ErrUnexpectedEOF, got %v", err)
	}
}
