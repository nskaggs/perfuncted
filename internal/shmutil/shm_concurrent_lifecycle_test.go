package shmutil

import (
	"fmt"
	"io"
	"os"
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
