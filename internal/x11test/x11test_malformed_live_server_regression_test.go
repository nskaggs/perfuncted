package x11test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A server that sends a malformed displayfd response and keeps running must
// be terminated promptly rather than blocking on cmd.Wait indefinitely.
func TestMalformedDisplayResponseStopsLiveServer(t *testing.T) {
	dir := t.TempDir()
	server := filepath.Join(dir, "Xvfb")
	if err := os.WriteFile(server, []byte("#!/bin/sh\nprintf 'invalid-display\\n' >&3\nexec sleep 2\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	start := time.Now()
	display, stop, err := StartXvfb()
	if display != "" || stop != nil || err == nil || !strings.Contains(err.Error(), "did not report a display") {
		t.Fatalf("malformed displayfd accepted: display=%q stop=%v err=%v", display, stop != nil, err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("invalid displayfd waited %v for live server to exit", elapsed)
	}
}
