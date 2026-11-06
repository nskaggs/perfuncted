package clipboard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The public Set entry point, not merely the owner helper, must fail closed
// when a configured Wayland owner executable disappears after discovery.
func TestWaylandSetMissingOwnerLeavesNoPrivateArtifacts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	backend := &extCmdClipboard{setCmd: []string{filepath.Join(dir, "missing", "wl-copy"), "--foreground"}, getCmd: []string{"/bin/false"}}
	const payload = "private clipboard selection"
	err := backend.Set(context.Background(), payload)
	if !errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), payload) {
		t.Fatalf("missing owner returned unexpected error: %v", err)
	}
	for _, pattern := range []string{"perfuncted-clipboard-input-*", "perfuncted-clipboard-stderr-*"} {
		matches, globErr := filepath.Glob(filepath.Join(dir, pattern))
		if globErr != nil || len(matches) != 0 {
			t.Fatalf("staging leaked for %s: %v %v", pattern, matches, globErr)
		}
	}
}

// No clipboard command may run after cancellation, even if the caller invokes
// the readiness function directly with a previous read configuration.
func TestWaylandReadinessPreCanceledNeverObservesSelection(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	tool := filepath.Join(dir, "wl-paste")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nprintf 'ran' > '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend := &extCmdClipboard{getCmd: []string{tool}}
	if err := backend.waitWaylandContent(ctx, "sensitive"); !errors.Is(err, context.Canceled) {
		t.Fatalf("readiness cancellation = %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled readiness executed paste: %v", err)
	}
}
