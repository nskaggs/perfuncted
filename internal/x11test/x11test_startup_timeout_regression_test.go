package x11test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A display server that inherits displayfd but never publishes a display must
// be terminated at the deadline, not waited on until it exits voluntarily.
func TestDisplayStartupTimeoutTerminatesUnresponsiveServer(t *testing.T) {
	directory := t.TempDir()
	server := filepath.Join(directory, "Xvfb")
	if err := os.WriteFile(server, []byte("#!/bin/sh\nexec sleep 2\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	started := time.Now()
	display, stop, err := startServerWithTimeout("Xvfb", "", func() []string { return nil }, 50*time.Millisecond)
	elapsed := time.Since(started)
	if display != "" || stop != nil || err == nil || !strings.Contains(err.Error(), "did not become ready") {
		t.Fatalf("unexpected timeout result: display=%q stop=%v err=%v", display, stop != nil, err)
	}
	if elapsed > time.Second {
		t.Fatalf("startup timeout waited %s for an unresponsive server", elapsed)
	}
}
