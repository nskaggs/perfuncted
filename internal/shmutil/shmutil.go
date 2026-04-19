// Package shmutil provides shared-memory file creation for Wayland protocols.
// Both the screen capture and virtual keyboard backends need anonymous temp
// files in XDG_RUNTIME_DIR for wl_shm buffers and XKB keymaps. This package
// deduplicates that logic.
package shmutil

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// CreateFile attempts to create an anonymous memfd-backed file (Linux) and
// fallbacks to an unlinked temp file in XDG_RUNTIME_DIR. The returned *os.File
// is responsible for closing the FD. Using memfd avoids leaving names in /dev/shm
// if the process crashes before cleanup.
func CreateFile(size int64) (*os.File, error) {
	// Try memfd_create first (preferred)
	if fd, err := unix.MemfdCreate("perfuncted-shm", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING); err == nil {
		if err := unix.Ftruncate(fd, size); err == nil {
			// Wrap the FD in an *os.File and return. Name is empty (anonymous).
			f := os.NewFile(uintptr(fd), "")
			return f, nil
		}
		unix.Close(fd) //nolint:errcheck
	}

	// Fallback: create an unlinked temp file in XDG_RUNTIME_DIR
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		return nil, fmt.Errorf("XDG_RUNTIME_DIR not set")
	}
	f, err := os.CreateTemp(dir, "perfuncted-shm-*")
	if err != nil {
		return nil, err
	}
	if err := f.Truncate(size); err != nil {
		f.Close()
		return nil, err
	}
	os.Remove(f.Name()) //nolint:errcheck
	return f, nil
}
