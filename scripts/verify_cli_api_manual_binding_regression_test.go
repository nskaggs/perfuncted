package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A declared manual CLI alias must not hide the disappearance of its API
// counterpart. A synthetic module isolates the check from the real bundle.
func TestCLISyncCheckerRejectsMissingManualBinding(t *testing.T) {
	module := t.TempDir()
	docs := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":    "module github.com/nskaggs/perfuncted\n\ngo 1.23\n",
		"bundle.go": "package perfuncted\ntype ScreenBundle struct{}\ntype InputBundle struct{}\ntype WindowBundle struct{}\ntype ClipboardBundle struct{}\n",
	} {
		if err := os.WriteFile(filepath.Join(module, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(docs, "pf_window_keep_above.md"), []byte("# Keep above\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := verifyCLISync(module, docs); got != 1 {
		t.Fatalf("missing manual WindowBundle.SetKeepAboveByID binding status = %d, want 1", got)
	}
}
