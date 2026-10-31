package input

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

func TestEIClientNeverDeliversQueuedMessagesAfterCallerCanceled(t *testing.T) {
	for i := 0; i < 128; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c := &eiClient{messages: make(chan eiMessage, 1), done: make(chan struct{})}
		c.messages <- eiMessage{object: 17, opcode: 3, body: eiU32(42)}
		msg, err := c.next(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("iteration %d: canceled client delivered queued event %+v: %v", i, msg, err)
		}
	}
}

func TestEIReceivedFileDescriptorCleanupDoesNotLeakOrCloseUnrelatedDescriptors(t *testing.T) {
	// The client must discard and close all SCM_RIGHTS descriptors from an
	// untrusted peer. A descriptor is transferred to the reader, not shared.
	fds := make([]int, 2)
	if err := unix.Pipe(fds); err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fds[0])
	defer unix.Close(fds[1])
	incoming := make([]int, 4)
	for i := range incoming {
		dup, err := unix.Dup(fds[0])
		if err != nil {
			t.Fatal(err)
		}
		incoming[i] = dup
	}
	control := unix.UnixRights(incoming...)
	closeReceivedFDs(control)
	for _, fd := range incoming {
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
			t.Errorf("received descriptor %d was not closed: %v", fd, err)
			_ = unix.Close(fd)
		}
	}
	if _, err := unix.FcntlInt(uintptr(fds[0]), unix.F_GETFD, 0); err != nil {
		t.Fatalf("cleanup closed unrelated original descriptor: %v", err)
	}
	closeReceivedFDs([]byte{0, 1, 2})
}
