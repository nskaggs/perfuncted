package clipboard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A failed external clipboard tool can echo its stdin to stderr. Command
// diagnostics must explain the failure without putting the attempted private
// selection in a returned error or session-level logs.
func TestClipboardSetRedactsEchoedSelectionFromToolDiagnostics(t *testing.T) {
	const secret = "private-selection-9f3cc7"
	for _, tc := range []struct{ name, executable string }{
		{name: "generic owner", executable: "synthetic-owner"},
		{name: "xclip owner", executable: "xclip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.executable)
			script := "#!/bin/sh\nIFS= read -r value\nprintf 'owner rejected selection: %s; retry later\\n' \"$value\" >&2\nexit 43\n"
			if err := os.WriteFile(path, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			backend := &extCmdClipboard{setCmd: []string{path}}
			err := backend.Set(context.Background(), secret)
			if err == nil || !strings.Contains(err.Error(), "exit status 43") || !strings.Contains(err.Error(), "retry later") {
				t.Fatalf("command failure diagnostics lost: %v", err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("clipboard content leaked through tool stderr: %v", err)
			}
		})
	}
}
