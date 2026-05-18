package shmutil

import (
	"io"
	"testing"
)

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
