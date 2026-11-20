package main

import (
	"path/filepath"
	"testing"
)

// An invalid documentation glob must be a hard verification failure rather
// than silently interpreting the documentation set as empty.
func TestCLISyncCheckerRejectsMalformedDocumentationGlob(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	if got := verifyCLISync(root, "[invalid-glob"); got != 2 {
		t.Fatalf("malformed documentation glob status = %d, want 2", got)
	}
}
