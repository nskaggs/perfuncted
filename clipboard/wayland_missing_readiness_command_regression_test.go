package clipboard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A Wayland clipboard owner alone is not proof that the compositor accepted
// the new selection. Without a paste probe the result cannot be verified, and
// the owner must not be started for an incomplete backend.
func TestWaylandClipboardSetRejectsMissingReadinessCommandBeforeOwnerSpawn(t *testing.T) {
	dir := t.TempDir()
	owner := filepath.Join(dir, "wl-copy")
	marker := filepath.Join(dir, "launched")
	if err := os.WriteFile(owner, []byte("#!/bin/sh\nprintf started > \""+marker+"\"\nexec sleep 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	backend := &extCmdClipboard{setCmd: []string{owner, "--foreground"}}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	err := backend.Set(ctx, "private clipboard value")
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("Wayland clipboard accepted unobservable selection: %v", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("unverifiable clipboard owner was launched: %v", statErr)
	}
}
