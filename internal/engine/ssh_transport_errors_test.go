package engine

import (
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"
)

// TestIsSSHConnTransientError ...
func TestIsSSHConnTransientError(t *testing.T) {
	t.Parallel()
	if IsSSHConnTransientError(nil) {
		t.Fatal("nil should not be transient")
	}
	if IsSSHConnTransientError(fmt.Errorf("exit status 1")) {
		t.Fatal("generic error should not be transient")
	}
	addrErr := &net.OpError{Op: "read", Net: "tcp", Err: syscall.EADDRNOTAVAIL}
	if !IsSSHConnTransientError(addrErr) {
		t.Fatal("EADDRNOTAVAIL OpError should be transient")
	}
	resetErr := &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}
	if !IsSSHConnTransientError(resetErr) {
		t.Fatal("ECONNRESET should be transient")
	}
	eofWrapped := &net.OpError{Op: "read", Net: "tcp", Err: io.EOF}
	if !IsSSHConnTransientError(eofWrapped) {
		t.Fatal("EOF in OpError should be transient")
	}
	// macOS / Linux wording seen on read path
	if !IsSSHConnTransientError(errors.New(`read tcp 192.168.1.2:12345->10.0.0.1:22: read: can't assign requested address`)) {
		t.Fatal("string match for can't assign should be transient")
	}
}
