package proxy

import (
	"context"
	"io"
	"net"
	"testing"
)

func TestDirectDialer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		io.Copy(conn, conn) // echo
	}()

	d := DirectDialer{}
	conn, err := d.DialContext(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	_, err = conn.Write([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 5)
	_, err = io.ReadFull(conn, buf)
	if err != nil {
		t.Fatal(err)
	}

	if string(buf) != "hello" {
		t.Fatalf("expected hello, got %s", buf)
	}
}
