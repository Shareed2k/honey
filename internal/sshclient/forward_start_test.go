package sshclient

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestStartLocalForward_errors(t *testing.T) {
	_, _, _, err := StartLocalForward(context.Background(), nil, "127.0.0.1", 8080, "h", 80)
	if err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestStartLocalForward_listenStop(t *testing.T) {
	client, cleanup := newLoopbackSSHClient(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, listenPort, stop, err := StartLocalForward(ctx, client, "127.0.0.1", 0, "127.0.0.1", 9)
	if err != nil {
		t.Fatal(err)
	}
	if listenPort <= 0 {
		t.Fatalf("listenPort: %d", listenPort)
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(listenPort)), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	stop()
}

func TestBridgeConns(t *testing.T) {
	a, b := net.Pipe()
	c, d := net.Pipe()
	go func() {
		_, _ = a.Write([]byte("ping"))
	}()
	go bridgeConns(b, c)
	buf := make([]byte, 4)
	_ = d.SetReadDeadline(time.Now().Add(time.Second))
	n, err := d.Read(buf)
	if err != nil || string(buf[:n]) != "ping" {
		t.Fatalf("read %q err=%v", buf[:n], err)
	}
	_ = a.Close()
	_ = d.Close()
}

func newLoopbackSSHClient(t *testing.T) (*ssh.Client, func()) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	serverCfg := &ssh.ServerConfig{NoClientAuth: true}
	serverCfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	go func() {
		defer close(stop)
		for {
			raw, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func(raw net.Conn) {
				_, chans, reqs, serr := ssh.NewServerConn(raw, serverCfg)
				if serr != nil {
					_ = raw.Close()
					return
				}
				go ssh.DiscardRequests(reqs)
				for newCh := range chans {
					switch newCh.ChannelType() {
					case "direct-tcpip":
						ch, inReqs, chErr := newCh.Accept()
						if chErr != nil {
							continue
						}
						go ssh.DiscardRequests(inReqs)
						_, _ = io.Copy(io.Discard, ch)
						_ = ch.Close()
					default:
						_ = newCh.Reject(ssh.UnknownChannelType, "unsupported")
					}
				}
			}(raw)
		}
	}()

	client, err := ssh.Dial("tcp", ln.Addr().String(), &ssh.ClientConfig{
		User:            "test",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return client, func() {
		_ = client.Close()
		_ = ln.Close()
		<-stop
	}
}
