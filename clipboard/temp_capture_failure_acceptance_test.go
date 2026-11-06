package clipboard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A failed temporary-file allocation must not execute a clipboard owner or
// disclose the selection bytes in diagnostics, regardless of backend mode.
func TestClipboardTempFileFailureNeverRunsSelectionOwner(t *testing.T) {
	for _, tc := range []struct{ name, tool string }{
		{name: "xclip diagnostic capture", tool: "xclip"},
		{name: "wayland input staging", tool: "wl-copy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			marker := filepath.Join(root, "owner-executed")
			bin := filepath.Join(root, tc.tool)
			if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf ran >"+marker+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TMPDIR", filepath.Join(root, "nonexistent-temporary-directory"))
			backend := &extCmdClipboard{setCmd: []string{bin}, getCmd: []string{"false"}}
			const privateContent = "confidential clipboard contents"
			err := backend.Set(context.Background(), privateContent)
			if !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "clipboard set:") {
				t.Fatalf("temporary capture error = %v", err)
			}
			if strings.Contains(err.Error(), privateContent) {
				t.Fatalf("error disclosed clipboard contents: %v", err)
			}
			if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("clipboard owner unexpectedly executed: %v", statErr)
			}
		})
	}
}
