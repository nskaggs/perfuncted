package input

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeX11PeerFailsClosed(t *testing.T) {
	for _, display := range []string{"localhost:0", ":", ":-1", ":999999", ":00", "/:0", ":xyz", ":1.2.3", ":1.abc", ":1.-1", ":1.01"} {
		err := nativeX11Peer(display)
		if !errors.Is(err, ErrObservationUnsupported) {
			t.Fatalf("%q: %v", display, err)
		}
	}
	if err := nativeX11Peer(":65534"); !errors.Is(err, ErrObservationUnavailable) {
		t.Fatalf("missing server: %v", err)
	}
}

func TestNativeX11PeerRejectsUnrecognizedExecutable(t *testing.T) {
	// Own one high-numbered Unix socket for the duration of this test;
	// never unlink or alter any preexisting X11 server socket.
	dir := "/tmp/.X11-unix"
	n := 50000 + os.Getpid()%10000
	path := filepath.Join(dir, fmt.Sprintf("X%d", n))
	if _, err := os.Lstat(path); err == nil {
		t.Skipf("unrelated X11 socket already exists: %s", path)
	} else if !os.IsNotExist(err) {
		t.Skip(err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Skipf("cannot own isolated local X11 socket: %v", err)
	}
	defer listener.Close()
	accepted := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			close(accepted)
			time.Sleep(50 * time.Millisecond)
			_ = conn.Close()
		}
	}()
	err = nativeX11Peer(fmt.Sprintf(":%d", n))
	if !errors.Is(err, ErrObservationUnsupported) {
		t.Fatalf("non-Xorg process accepted: %v", err)
	}
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("peer was not accepted")
	}
}
