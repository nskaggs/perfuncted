package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The ledger must cover every runnable command, specify a real test or
// mandatory native script, and never use an unmapped placeholder. A source
// reference is not proof of an assertion; its strength is explicit.
func TestReleaseCommandCoverageInventory(t *testing.T) {
	root := newRootCmd(defaultOpenPFFactory)
	root.InitDefaultCompletionCmd()
	raw, err := os.ReadFile("testdata/release-command-coverage.tsv")
	if err != nil {
		t.Fatal(err)
	}
	ledger := map[string]string{}
	contracts := releaseAcceptanceTestNames(t, ".")
	live := releaseAcceptanceTestNames(t, "../../integration")
	packaged := releaseAcceptanceTestNames(t, "../../flatpaktest")
	nativeScript, err := os.ReadFile("../../scripts/verify-kde-wayland-automation.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		path, status, ok := strings.Cut(line, "\t")
		if !ok || path == "" || status == "" || ledger[path] != "" {
			t.Fatalf("invalid inventory row %q", line)
		}
		checkReleaseEvidence(t, path, status, contracts, live, packaged, string(nativeScript))
		ledger[path] = status
	}
	counts := map[string]int{}
	var visit func(*cobra.Command)
	visit = func(cmd *cobra.Command) {
		if cmd.Hidden || cmd.Name() == "help" {
			return
		}
		if cmd.Runnable() {
			path := strings.TrimPrefix(cmd.CommandPath(), "pf ")
			status, ok := ledger[path]
			if !ok {
				t.Errorf("new command requires acceptance evidence: %s", path)
			} else {
				tier, _, _ := strings.Cut(status, ":")
				counts[tier]++
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
	t.Logf("complete command inventory by evidence tier: %v", counts)
}

func releaseAcceptanceTestNames(t *testing.T, dir string) map[string]bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, path := range files {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") {
				names[fn.Name.Name] = true
			}
		}
	}
	return names
}

func checkReleaseEvidence(t *testing.T, path, status string, contracts, live, packaged map[string]bool, nativeScript string) {
	t.Helper()
	tier, refs, ok := strings.Cut(status, ":")
	if !ok || refs == "" {
		t.Errorf("command %s lacks acceptance evidence: %s", path, status)
		return
	}
	var names map[string]bool
	switch tier {
	case "contract", "live-cobra", "headless-x11":
		names = contracts
	case "matrix", "wayland-motion-and-x11-rejection", "x11-live-wayland-rejection", "x11", "headless-wayland", "native-atspi":
		names = live
	case "packaged-live":
		names = packaged
	case "native-kde":
		if refs != "verify-kde-wayland-automation.sh" || !strings.Contains(nativeScript, "\"$PF_BIN\" "+path) {
			t.Errorf("command %s lacks native KDE script invocation", path)
		}
		return
	default:
		t.Errorf("command %s has unknown evidence tier %q", path, tier)
		return
	}
	for _, name := range strings.Split(refs, "/") {
		if !names[name] {
			t.Errorf("command %s refers to missing %s test %s", path, tier, name)
		}
	}
}
