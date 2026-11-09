package shmutil

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func forceMemfdUnavailable() (int, error) { return -1, unix.ENOSYS }

func TestSharedMemoryFilesystemFallbackOwnsUnlinkedPrivateFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	file, err := createFileWithMemfd(64, forceMemfdUnavailable)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() != 64 || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatalf("fallback file has wrong properties: %v %v", info, err)
	}
	flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatalf("fallback descriptor inheritable: flags=%d err=%v", flags, err)
	}
	if _, writeErr := file.WriteAt([]byte("private"), 57); writeErr != nil {
		t.Fatal(writeErr)
	}
	buf := make([]byte, 7)
	if _, readErr := file.ReadAt(buf, 57); readErr != nil && !errors.Is(readErr, io.EOF) {
		t.Fatal(readErr)
	}
	if string(buf) != "private" {
		t.Fatalf("fallback contents=%q", buf)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("fallback leaked named files: %v %v", entries, err)
	}
}

func TestSharedMemoryFilesystemFallbackRejectsUnsafeStorageFailures(t *testing.T) {
	for _, tc := range []struct {
		name, root string
		size       int64
	}{
		{name: "missing runtime", size: 16},
		{name: "not a directory", size: 16},
		{name: "invalid length", size: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			switch tc.name {
			case "missing runtime":
				t.Setenv("XDG_RUNTIME_DIR", "")
			case "not a directory":
				file := filepath.Join(dir, "ordinary")
				if err := os.WriteFile(file, []byte("not a directory"), 0600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("XDG_RUNTIME_DIR", file)
			case "invalid length":
				t.Setenv("XDG_RUNTIME_DIR", dir)
			}
			file, err := createFileWithMemfd(tc.size, forceMemfdUnavailable)
			if file != nil {
				file.Close()
			}
			if err == nil || file != nil {
				t.Fatalf("invalid fallback succeeded: file=%v err=%v", file, err)
			}
			if tc.name == "invalid length" {
				entries, readErr := os.ReadDir(dir)
				if readErr != nil || len(entries) != 0 {
					t.Fatalf("failed fallback leaked files: %v %v", entries, readErr)
				}
			}
		})
	}
}
