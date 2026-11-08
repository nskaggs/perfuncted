//go:build linux

package perfuncted

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Stale-session cleanup must never wait for a FIFO writer to provide a PID.
// Both owner and helper metadata are untrusted files inside a runtime tree.
func TestSessionCleanupPIDMetadataRejectsFIFOWithoutBlocking(t *testing.T) {
	for _, name := range []string{sessionOwnerPIDFile, "dbus.pid"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			fifo := filepath.Join(dir, name)
			if err := unix.Mkfifo(fifo, 0600); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				if name == sessionOwnerPIDFile {
					_ = reapSessionDirIfStale(context.Background(), dir, time.Hour, time.Now())
				} else {
					_, _ = readPIDFile(fifo)
				}
			}()
			select {
			case <-done:
			case <-time.After(200 * time.Millisecond):
				t.Fatalf("PID metadata %q blocked waiting for FIFO writer", name)
			}
			fi, err := os.Lstat(fifo)
			if err != nil || fi.Mode()&os.ModeNamedPipe == 0 {
				t.Fatalf("foreign FIFO changed: %v %v", fi, err)
			}
		})
	}
}

// The PID marker is authorization evidence for deleting runtime directories.
// Never follow an untrusted symlink or accept arbitrarily large metadata.
func TestSessionCleanupPIDMetadataRejectsSymlinksAndOversize(t *testing.T) {
	dir := t.TempDir()
	foreign := filepath.Join(dir, "unrelated")
	if err := os.WriteFile(foreign, []byte("4242\n"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "helper.pid")
	if err := os.Symlink(foreign, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readPIDFile(link); err == nil {
		t.Fatal("symlinked PID authorized")
	}
	before, readErr := os.ReadFile(foreign)
	if readErr != nil || string(before) != "4242\n" {
		t.Fatalf("foreign target changed: %q %v", before, readErr)
	}
	tooLarge := filepath.Join(dir, "large.pid")
	if err := os.WriteFile(tooLarge, []byte(strings.Repeat("1", 1024*1024)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPIDFile(tooLarge); err == nil || !strings.Contains(err.Error(), "oversized") {
		t.Fatalf("oversized PID metadata accepted: %v", err)
	}
	valid := filepath.Join(dir, "valid.pid")
	if err := os.WriteFile(valid, []byte("  4242\n"), 0600); err != nil {
		t.Fatal(err)
	}
	pid, err := readPIDFile(valid)
	if err != nil || pid != 4242 {
		t.Fatalf("valid PID rejected: %d %v", pid, err)
	}
}
