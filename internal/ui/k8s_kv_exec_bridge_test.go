package ui

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"testing"
	"time"
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
	hdr[0] = byte(hkvFrameData)
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
	hdr[0] = byte(hkvFrameData)
	binary.BigEndian.PutUint32(hdr[1:5], 3)
	binary.BigEndian.PutUint32(hdr[5:9], 10)
	r := bytes.NewReader(append(hdr[:], []byte{1, 2, 3}...))
	_, _, _, err := readHkvFrame(r)
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("expected ErrUnexpectedEOF, got %v", err)
	}
}

// TestRunHkvConn_echo exercises the per-conn goroutine end-to-end against a local TCP echo: data sent
// via the inbox should arrive back as hkvFrameData frames on pwIn. Cancelling the ctx must shut the
// goroutine down promptly and emit a final close frame.
func TestRunHkvConn_echo(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		_, _ = io.Copy(c, c)
		_ = c.Close()
	}()

	pwInR, pwInW := io.Pipe()

	framesDone := make(chan struct{})
	got := make(chan []byte, 4)
	go func() {
		defer close(framesDone)
		for {
			typ, _, pay, ferr := readHkvFrame(pwInR)
			if ferr != nil {
				return
			}
			got <- append([]byte{byte(typ)}, pay...)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	e := &hkvConn{inbox: make(chan []byte, 4), cancel: cancel}
	var connsMu sync.Mutex
	conns := map[uint32]*hkvConn{42: e}
	closedByUs := make(map[uint32]struct{})
	var stdinMu sync.Mutex
	failed := make(chan error, 1)

	connDone := make(chan struct{})
	go func() {
		defer close(connDone)
		runHkvConn(ctx, ln.Addr().String(), 42, e, &stdinMu, pwInW, &connsMu, conns, closedByUs, func(err error) {
			select {
			case failed <- err:
			default:
			}
		})
	}()

	e.inbox <- []byte("ping")
	select {
	case f := <-got:
		if f[0] != byte(hkvFrameData) || string(f[1:]) != "ping" {
			t.Fatalf("unexpected frame: typ=%d pay=%q", f[0], f[1:])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for echo frame")
	}

	cancel()
	select {
	case <-connDone:
	case <-time.After(2 * time.Second):
		t.Fatal("runHkvConn did not exit after cancel")
	}
	_ = pwInW.Close()
	<-framesDone

	// Last frame must be a Close for cid 42 (closedByUs was empty when the goroutine exited).
	connsMu.Lock()
	_, marked := closedByUs[42]
	connsMu.Unlock()
	if !marked {
		t.Fatal("expected closedByUs to be set for cid 42 after natural exit")
	}
	select {
	case err := <-failed:
		t.Fatalf("unexpected fail: %v", err)
	default:
	}
}
