package clipboard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A failed owner can print only a *part* of its stdin. Substituting the exact
// clipboard text in stderr does not sanitize these private fragments.
func TestClipboardSetDoesNotLeakPartialContentFromFailedOwner(t *testing.T) {
	const private = "private-selection-very-secret-42"
	const fragment = "selection-very-secret"
	for _, name := range []string{"generic-owner", "xclip"} {
		t.Run(name, func(t *testing.T) {
			executable := filepath.Join(t.TempDir(), name)
			if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf 'owner failed on: selection-very-secret\\n' >&2\nexit 47\n"), 0700); err != nil {
				t.Fatal(err)
			}
			backend := &extCmdClipboard{setCmd: []string{executable}}
			err := backend.Set(context.Background(), private)
			if err == nil || !strings.Contains(err.Error(), "exit status 47") || strings.Contains(err.Error(), fragment) {
				t.Fatalf("private clipboard fragment disclosed: err=%v", err)
			}
		})
	}
}
