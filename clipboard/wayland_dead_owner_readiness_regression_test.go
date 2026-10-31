package clipboard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A previously populated selection may already have the exact requested text.
// That must not make Set succeed if the foreground wl-copy owner died before
// it took ownership: no new selection was established by this invocation.
func TestWaylandSetDoesNotAcceptStaleMatchingSelectionAfterOwnerExit(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	owner := filepath.Join(dir, "wl-copy")
	paste := filepath.Join(dir, "wl-paste")
	if err := os.WriteFile(owner, []byte("#!/bin/sh\nexit 23\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paste, []byte("#!/bin/sh\nsleep 0.15\nprintf 'old-selection'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cb := &extCmdClipboard{setCmd: []string{owner, "--foreground"}, getCmd: []string{paste, "--no-newline"}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := cb.Set(ctx, "old-selection"); err == nil || !strings.Contains(err.Error(), "owner") {
		t.Fatalf("Set falsely reported readiness from stale matching selection: %v", err)
	}
	for _, glob := range []string{"perfuncted-clipboard-input-*", "perfuncted-clipboard-stderr-*"} {
		files, err := filepath.Glob(filepath.Join(dir, glob))
		if err != nil || len(files) > 0 {
			t.Fatalf("temporary clipboard files leaked for %s: %v %v", glob, files, err)
		}
	}
}

// A still-living foreground owner with an observable selection must continue
// to satisfy the ordinary Set path, despite explicit liveness checking.
func TestWaylandSetAcceptsMatchingSelectionFromLiveOwner(t *testing.T) {
	dir := t.TempDir()
	owner := filepath.Join(dir, "wl-copy")
	paste := filepath.Join(dir, "wl-paste")
	if err := os.WriteFile(owner, []byte("#!/bin/sh\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paste, []byte("#!/bin/sh\nprintf 'new-selection'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cb := &extCmdClipboard{setCmd: []string{owner, "--foreground"}, getCmd: []string{paste, "--no-newline"}}
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	if err := cb.Set(ctx, "new-selection"); err != nil {
		t.Fatalf("live foreground owner rejected: %v", err)
	}
}
