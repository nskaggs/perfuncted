package clipboard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Clipboard readers can put the current selection in stderr when failing.
// That data is unknown to Get, so returning unsanitized stderr leaks it into
// callers' errors and logs.
func TestExternalClipboardGetDoesNotExposeSelectionFromFailureStderr(t *testing.T) {
	const selection = "secret-clipboard-selection-6f4a"
	script := filepath.Join(t.TempDir(), "reader")
	content := "#!/bin/sh\nprintf 'reader failed while accessing %s\\n' '" + selection + "' >&2\nexit 27\n"
	if err := os.WriteFile(script, []byte(content), 0700); err != nil {
		t.Fatal(err)
	}
	cb := &extCmdClipboard{getCmd: []string{script}}
	got, err := cb.Get(context.Background())
	if err == nil || got != "" || !strings.Contains(err.Error(), "exit status 27") {
		t.Fatalf("failed reader not reported: result=%q err=%v", got, err)
	}
	if strings.Contains(err.Error(), selection) {
		t.Fatalf("clipboard selection leaked through stderr in returned error: %v", err)
	}
}
