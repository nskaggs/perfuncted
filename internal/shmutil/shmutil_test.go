package shmutil

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestCreateFileConcurrentIndependentLifetime(t *testing.T) {
	const workers = 24
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	var wg sync.WaitGroup
	failures := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			f, err := CreateFile(64)
			if err != nil {
				failures <- fmt.Errorf("create %d: %w", i, err)
				return
			}
			defer f.Close()
			payload := []byte(fmt.Sprintf("worker-%02d", i))
			if _, err := f.WriteAt(payload, 0); err != nil {
				failures <- err
				return
			}
			got := make([]byte, len(payload))
			if _, err := f.ReadAt(got, 0); err != nil && err != io.EOF {
				failures <- err
				return
			}
			if string(got) != string(payload) {
				failures <- fmt.Errorf("worker %d read %q", i, got)
			}
		}(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("shared-memory files leaked: %v", entries)
	}
}

func TestCreateFileZeroSizeAndClosedDescriptor(t *testing.T) {
	f, err := CreateFile(0)
	if err != nil {
		t.Fatal(err)
	}
	stat, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if stat.Size() != 0 {
		t.Fatalf("zero length became %d", stat.Size())
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("x")); err == nil {
		t.Fatal("closed descriptor remained writable")
	}
}

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

func TestCreateFileRejectsNegativeSizeAndCleansFallbackArtifacts(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", directory)
	file, err := CreateFile(-1)
	if err == nil || file != nil {
		t.Fatalf("negative shared memory size accepted: file=%v error=%v", file, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed mmap left temporary artifacts: %v", entries)
	}
}

func TestCreateFileFailureWithoutRuntimeDirectory(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	file, err := CreateFile(-1)
	if err == nil || file != nil {
		t.Fatalf("negative shared memory length without runtime directory: %v, %v", file, err)
	}
}

func TestCreateFileReportsInvalidRuntimeDirectory(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(t.TempDir(), "not-present"))
	file, err := CreateFile(-1)
	if err == nil || file != nil {
		t.Fatalf("invalid runtime directory accepted: %v, %v", file, err)
	}
}

func TestCreateFileCreatesSizedWritableFile(t *testing.T) {
	t.Parallel()

	f, err := CreateFile(16)
	if err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() != 16 {
		t.Fatalf("size = %d, want 16", info.Size())
	}

	if _, err := f.WriteAt([]byte("ok"), 14); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	buf := make([]byte, 2)
	if _, err := f.ReadAt(buf, 14); err != nil && err != io.EOF {
		t.Fatalf("ReadAt: %v", err)
	}
	if string(buf) != "ok" {
		t.Fatalf("read = %q, want ok", string(buf))
	}
}
