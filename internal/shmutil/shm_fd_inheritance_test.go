package shmutil

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// Shared-memory descriptors must not leak into clipboard or desktop helper
// processes started after the buffer has been allocated.
func TestCreateFileDescriptorsAreCloseOnExec(t *testing.T) {
	for _, size := range []int64{0, 4096} {
		f, err := CreateFile(size)
		if err != nil {
			t.Fatalf("CreateFile(%d): %v", size, err)
		}
		flags, err := unix.FcntlInt(f.Fd(), unix.F_GETFD, 0)
		if err != nil {
			f.Close()
			t.Fatalf("F_GETFD(%d): %v", size, err)
		}
		if flags&unix.FD_CLOEXEC == 0 {
			f.Close()
			t.Fatalf("CreateFile(%d) descriptor can leak across exec", size)
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			t.Fatalf("Stat(%d): %v", size, err)
		}
		if info.Size() != size {
			f.Close()
			t.Fatalf("CreateFile(%d) size = %d", size, info.Size())
		}
		if err := f.Close(); err != nil {
			t.Fatalf("Close(%d): %v", size, err)
		}
		if _, err := f.Stat(); !errors.Is(err, os.ErrClosed) {
			t.Fatalf("descriptor remains valid after close: %v", err)
		}
	}
}
