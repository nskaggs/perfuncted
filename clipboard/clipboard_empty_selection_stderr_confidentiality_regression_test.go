package clipboard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An empty clipboard write does not make the previous selection public.
// Some failing clipboard tools include the old selection in stderr.
func TestClipboardSetEmptyDoesNotLeakPreviousSelectionFromOwnerStderr(t *testing.T) {
	const previousSelection = "previous-secret-selection-f7d2"
	for _, name := range []string{"synthetic-owner", "xclip"} {
		t.Run(name, func(t *testing.T) {
			executable := filepath.Join(t.TempDir(), name)
			script := "#!/bin/sh\nprintf 'previous clipboard: previous-secret-selection-f7d2\\n' >&2\nexit 49\n"
			if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			backend := &extCmdClipboard{setCmd: []string{executable}}
			err := backend.Set(context.Background(), "")
			if err == nil || !strings.Contains(err.Error(), "exit status 49") || strings.Contains(err.Error(), previousSelection) {
				t.Fatalf("previous selection leaked through failed empty write: %v", err)
			}
		})
	}
}
