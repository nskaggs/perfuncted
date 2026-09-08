package clipboard

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWaylandSetDoesNotWaitForPasteConsumer(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "clipboard.txt")
	tool := filepath.Join(dir, "wl-copy")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\ncat > \"$PF_TEST_CLIPBOARD_OUTPUT\"\nsleep 30\n"), 0o700); err != nil {
		t.Fatalf("write fake wl-copy: %v", err)
	}

	cb := &extCmdClipboard{
		setCmd: []string{tool, "--foreground", "--paste-once"},
		env:    append(os.Environ(), "PF_TEST_CLIPBOARD_OUTPUT="+output),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := cb.Set(ctx, "browser console command"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 100*time.Millisecond {
		t.Fatalf("Set waited %s for the paste consumer, want an asynchronous return", elapsed)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(output); err == nil && string(data) == "browser console command" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("fake wl-copy did not receive clipboard content")
}
