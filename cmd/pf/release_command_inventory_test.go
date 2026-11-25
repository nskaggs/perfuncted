package main

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Enumerate the command tree itself rather than relying on generated docs or
// the current test names. A new command must be consciously classified.
func TestReleaseCommandCoverageInventory(t *testing.T) {
	root := newRootCmd(defaultOpenPFFactory)
	root.InitDefaultCompletionCmd()
	raw, err := os.ReadFile("testdata/release-command-coverage.tsv")
	if err != nil {
		t.Fatal(err)
	}
	ledger := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		path, status, ok := strings.Cut(line, "\t")
		if !ok || path == "" || status == "" || ledger[path] != "" {
			t.Fatalf("invalid inventory row %q", line)
		}
		ledger[path] = status
	}
	total, unmapped := 0, 0
	var visit func(*cobra.Command)
	visit = func(cmd *cobra.Command) {
		if cmd.Hidden || cmd.Name() == "help" {
			return
		}
		if cmd.Runnable() {
			path := strings.TrimPrefix(cmd.CommandPath(), "pf ")
			status, ok := ledger[path]
			if !ok {
				t.Errorf("new command requires acceptance classification: %s", path)
			} else {
				total++
				if status == "unmapped" {
					unmapped++
				}
				delete(ledger, path)
			}
		}
		for _, child := range cmd.Commands() {
			visit(child)
		}
	}
	visit(root)
	for path := range ledger {
		t.Errorf("stale inventory command: %s", path)
	}
	t.Logf("%d runnable commands; %d mapped to named acceptance scenarios; %d still unmapped", total, total-unmapped, unmapped)
}
