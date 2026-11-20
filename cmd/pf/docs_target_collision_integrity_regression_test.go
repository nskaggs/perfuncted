package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Documentation generation must propagate a file collision instead of
// reporting success or replacing an unrelated directory at its output path.
func TestDocsGenerationRefusesExistingDirectoryAtMarkdownFile(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "pf_docs.md")
	if err := os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(blocked, "keep")
	if err := os.WriteFile(sentinel, []byte("unrelated data"), 0600); err != nil {
		t.Fatal(err)
	}
	root := &cobra.Command{Use: "pf"}
	root.AddCommand(docsCmd(root))
	root.SetArgs([]string{"docs", "--dir", dir})
	if err := root.Execute(); err == nil {
		t.Fatal("docs generation succeeded despite a directory collision")
	}
	content, err := os.ReadFile(sentinel)
	if err != nil || strings.TrimSpace(string(content)) != "unrelated data" {
		t.Fatalf("failed generation damaged existing output: %q %v", content, err)
	}
}
