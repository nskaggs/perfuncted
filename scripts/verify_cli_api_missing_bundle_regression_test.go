package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The CLI/API contract must fail closed if a bundle API disappears entirely.
// Otherwise an empty documentation directory could make a broken package pass.
func TestCLISyncCheckerRejectsMissingBundleTypes(t *testing.T) {
	module := t.TempDir()
	if err := os.WriteFile(filepath.Join(module, "go.mod"), []byte("module github.com/nskaggs/perfuncted\n\ngo 1.23\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module, "bundle.go"), []byte("package perfuncted\ntype ScreenBundle struct{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := verifyCLISync(module, t.TempDir()); got != 2 {
		t.Fatalf("missing InputBundle, WindowBundle and ClipboardBundle accepted: exit=%d, want 2", got)
	}
}
