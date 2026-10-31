package input

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestEIReadLoopClosesUnexpectedReceivedDescriptors(t *testing.T) {
	clientConn, serverConn := unixConnectionPair(t)
	t.Cleanup(func() { _ = serverConn.Close() })
	var pipe [2]int
	if err := unix.Pipe2(pipe[:], unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(pipe[0]) })
	t.Cleanup(func() { _ = unix.Close(pipe[1]) })

	client := newEIClient(clientConn)
	t.Cleanup(func() { _ = client.close() })
	message := eiRequest(eiServerIDFloor+1, 1, eiU32(1))
	if _, controlBytes, err := serverConn.WriteMsgUnix(message, unix.UnixRights(pipe[1]), nil); err != nil {
		t.Fatalf("send EIS message with unexpected descriptor: %v", err)
	} else if controlBytes == 0 {
		t.Fatal("descriptor control message was not sent")
	}
	got, err := client.next(t.Context())
	if err != nil {
		t.Fatalf("receive EIS event: %v", err)
	}
	if got.object != eiServerIDFloor+1 || got.opcode != 1 {
		t.Fatalf("received wrong EIS event: %+v", got)
	}
	if err := unix.Close(pipe[1]); err != nil {
		t.Fatal(err)
	}
	pipe[1] = -1
	var data [1]byte
	n, err := unix.Read(pipe[0], data[:])
	if err != nil || n != 0 {
		t.Fatalf("receiver retained an unsolicited descriptor: read = (%d, %v), want EOF", n, err)
	}
}
