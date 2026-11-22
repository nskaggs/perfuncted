package input

import (
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// ConnectToEIS is a byte stream. A zero-length datagram carrying a descriptor
// must terminate the reader and close the unsolicited descriptor instead of
// leaving the event loop spinning on an empty packet.
func TestEIReadLoopRejectsEmptyDatagramAndClosesTransferredDescriptor(t *testing.T) {
	sockets, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	peer := sockets[1]
	t.Cleanup(func() { _ = unix.Close(peer) })

	file := os.NewFile(uintptr(sockets[0]), "eis-datagram")
	if file == nil {
		t.Fatal("wrap EIS socket pair endpoint")
	}
	conn, err := net.FileConn(file)
	if closeErr := file.Close(); closeErr != nil {
		t.Fatalf("close original EIS descriptor: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("wrap EIS datagram socket: %v", err)
	}
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		_ = conn.Close()
		t.Fatalf("wrapped EIS socket has type %T, want *net.UnixConn", conn)
	}
	client := newEIClient(unixConn)
	t.Cleanup(func() { _ = client.close() })

	var pipe [2]int
	if err := unix.Pipe2(pipe[:], unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(pipe[0]) })
	t.Cleanup(func() { _ = unix.Close(pipe[1]) })
	if n, err := unix.SendmsgN(peer, nil, unix.UnixRights(pipe[1]), nil, 0); err != nil || n != 0 {
		t.Fatalf("send zero-payload descriptor packet: bytes=%d err=%v", n, err)
	}

	select {
	case <-client.done:
	case <-time.After(time.Second):
		t.Fatal("empty EIS datagram did not terminate the reader")
	}
	if err := client.readError(); err == nil || !strings.Contains(err.Error(), "peer closed the EIS stream") {
		t.Fatalf("empty EIS datagram error = %v", err)
	}
	if err := unix.Close(pipe[1]); err != nil {
		t.Fatal(err)
	}
	pipe[1] = -1
	var data [1]byte
	if n, err := unix.Read(pipe[0], data[:]); err != nil || n != 0 {
		t.Fatalf("reader retained transferred descriptor: read = (%d, %v), want EOF", n, err)
	}
}
