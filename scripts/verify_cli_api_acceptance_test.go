package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCLISyncCheckerValidatesRealRepositoryAndRejectsOutdatedDocs(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	if got := verifyCLISync(root, filepath.Join(root, "docs-cli")); got != 0 {
		t.Fatalf("real CLI documentation has API discrepancies; exit=%d", got)
	}
	docRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(docRoot, "pf_screen_totally-unimplemented-operation.md"), []byte("# bogus command\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := verifyCLISync(root, docRoot); got != 1 {
		t.Fatalf("unimplemented CLI docs were accepted: exit=%d", got)
	}
	if got := verifyCLISync(root, t.TempDir()); got != 1 {
		t.Fatalf("missing documented API commands were accepted: exit=%d", got)
	}
}

func TestCLISyncCheckerNeverFallsBackToUnrelatedModule(t *testing.T) {
	if got := verifyCLISync(t.TempDir(), t.TempDir()); got != 2 {
		t.Fatalf("unrelated module unexpectedly accepted: exit=%d", got)
	}
}
