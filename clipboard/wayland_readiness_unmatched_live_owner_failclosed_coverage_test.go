package clipboard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A foreground owner can remain alive without the compositor serving the
// requested selection. A successful launch is not a successful clipboard
// write: readiness mismatch must revoke and reap that owner.
func TestWaylandSetLiveOwnerWithUnmatchedSelectionFailsAndReaps(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	owner := filepath.Join(dir, "wl-copy")
	paste := filepath.Join(dir, "wl-paste")
	if err := os.WriteFile(owner, []byte("#!/bin/sh\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paste, []byte("#!/bin/sh\nprintf 'stale-selection'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cb := &extCmdClipboard{setCmd: []string{owner, "--foreground"}, getCmd: []string{paste, "--no-newline"}}
	err := cb.Set(context.Background(), "private-new-selection")
	if err == nil || !strings.Contains(err.Error(), "clipboard set") {
		t.Fatalf("unverified selection accepted: %v", err)
	}
	for _, pattern := range []string{"perfuncted-clipboard-input-*", "perfuncted-clipboard-stderr-*"} {
		matches, globErr := filepath.Glob(filepath.Join(dir, pattern))
		if globErr != nil || len(matches) != 0 {
			t.Fatalf("unverified owner leaked %s: %v %v", pattern, matches, globErr)
		}
	}
}
