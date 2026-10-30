package shmutil

import (
	"os"
	"path/filepath"
	"testing"
)

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
