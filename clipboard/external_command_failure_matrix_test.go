package clipboard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Failure messages must distinguish a failed external program from an empty
// selection, without publishing the attempted clipboard payload as diagnostics.
func TestExternalClipboardFailureDiagnosticMatrix(t *testing.T) {
	const secret = "private-selection-payload"
	for _, tc := range []struct{ name, script, suffix string }{
		{"silent failure", "exit 29", "exit status 29"},
		{"diagnostic failure", "printf 'backend unavailable\\n' >&2; exit 30", "exit status 30"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &extCmdClipboard{setCmd: []string{"sh", "-c", tc.script}}
			err := backend.Set(context.Background(), secret)
			if err == nil || !strings.Contains(err.Error(), tc.suffix) || strings.Contains(err.Error(), secret) {
				t.Fatalf("set failure was not safely diagnosed: %v", err)
			}
		})
	}
}

func TestXclipCaptureTemporaryFileIsRemovedOnSuccessAndFailure(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	for _, tc := range []struct {
		name, body string
		wantErr    bool
	}{
		{"success", "cat >/dev/null", false},
		{"failure", "printf 'backend rejected request' >&2; exit 31", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			program := filepath.Join(t.TempDir(), "xclip")
			if err := os.WriteFile(program, []byte("#!/bin/sh\n"+tc.body+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			backend := &extCmdClipboard{setCmd: []string{program}}
			err := backend.Set(context.Background(), "confidential-text")
			if (err != nil) != tc.wantErr {
				t.Fatalf("Set error = %v, want error %v", err, tc.wantErr)
			}
			if tc.wantErr && (!strings.Contains(err.Error(), "exit status 31") || strings.Contains(err.Error(), "backend rejected request") || strings.Contains(err.Error(), "confidential-text")) {
				t.Fatalf("unsafe diagnostics: %v", err)
			}
			entries, err := os.ReadDir(temp)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "perfuncted-clipboard-stderr-") {
					t.Fatalf("clipboard stderr capture was not removed: %s", entry.Name())
				}
			}
		})
	}
}
