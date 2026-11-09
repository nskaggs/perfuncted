package input

import (
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

// An untrusted EIS peer may send multiple ancillary records. Only SCM_RIGHTS
// descriptors belong to the receiver; unrelated control records must neither
// prevent their closure nor cause a different live descriptor to be closed.
func TestEISMixedAncillaryControlClosesOnlyTransferredDescriptors(t *testing.T) {
	var pipe [2]int
	if err := unix.Pipe2(pipe[:], unix.O_CLOEXEC); err != nil {
		t.Fatal(err)
	}
	defer unix.Close(pipe[0])
	defer unix.Close(pipe[1])
	first, err := unix.Dup(pipe[0])
	if err != nil {
		t.Fatal(err)
	}
	second, err := unix.Dup(pipe[1])
	if err != nil {
		unix.Close(first)
		t.Fatal(err)
	}
	control := append(unix.UnixCredentials(&unix.Ucred{Pid: 123, Uid: 234, Gid: 345}), unix.UnixRights(first, second)...)
	closeReceivedFDs(control)
	for _, fd := range []int{first, second} {
		if _, checkErr := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); !errors.Is(checkErr, unix.EBADF) {
			_ = unix.Close(fd)
			t.Fatalf("transferred descriptor %d remained open: %v", fd, checkErr)
		}
	}
	for _, fd := range pipe {
		if _, checkErr := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); checkErr != nil {
			t.Fatalf("original descriptor %d was closed: %v", fd, checkErr)
		}
	}
}
