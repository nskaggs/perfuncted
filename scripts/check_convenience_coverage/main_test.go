package coveragecheck

import (
	"strings"
	"testing"
)

func TestCheckCoverageExactFilesAndZeroStatementBlocks(t *testing.T) {
	const good = "mode: set\n" +
		"github.com/example/repo/convenience.go:1.1,2.1 2 1\n" +
		"github.com/example/repo/convenience.go:3.1,3.2 0 0\n" +
		"github.com/example/repo/convenience_window.go:1.1,2.1 1 2\n" +
		"github.com/example/repo/input/keysend.go:1.1,2.1 3 1\n" +
		"github.com/example/repo/other.go:1.1,2.1 5 0\n"
	summary, err := checkCoverage(strings.NewReader(good))
	if err != nil {
		t.Fatal(err)
	}
	if summary["convenience.go"].all != 2 || summary["convenience_window.go"].covered != 1 {
		t.Fatalf("incorrect coverage: %+v", summary)
	}
}

func TestCheckCoverageFailsOnUncoveredAndMissingFiles(t *testing.T) {
	cases := []string{
		"mode: set\n",
		"mode: set\ngithub.com/example/repo/convenience.go:1.1,2.1 2 1\n",
		"mode: set\ngithub.com/example/repo/convenience.go:1.1,2.1 2 0\n" +
			"github.com/example/repo/convenience_window.go:1.1,2.1 1 1\n",
		"mode: set\ngithub.com/example/repo/convenience.go:1.1,2.1 abc 1\n" +
			"github.com/example/repo/convenience_window.go:1.1,2.1 1 1\n",
		"mode: set\ngithub.com/example/repo/convenience.go:1.1,2.1 1 x\n" +
			"github.com/example/repo/convenience_window.go:1.1,2.1 1 1\n",
	}
	for _, content := range cases {
		if _, err := checkCoverage(strings.NewReader(content)); err == nil {
			t.Fatalf("invalid coverage accepted: %q", content)
		}
	}
}
