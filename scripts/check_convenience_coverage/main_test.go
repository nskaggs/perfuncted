package coveragecheck

import (
	"strings"
	"testing"
)

func TestCheckCoverageExactFilesAndZeroStatementBlocks(t *testing.T) {
	const good = "mode: set\n" +
		modulePrefix + "convenience.go:1.1,2.1 2 1\n" +
		modulePrefix + "convenience.go:3.1,3.2 0 0\n" +
		modulePrefix + "convenience.go:4.1,5.1 1 2\n" +
		modulePrefix + "input/keysend.go:1.1,2.1 3 1\n" +
		modulePrefix + "other/convenience.go:1.1,2.1 5 0\n" +
		modulePrefix + "other/keysend.go:1.1,2.1 5 0\n" +
		"github.com/example/repo/convenience.go:1.1,2.1 5 0\n"
	summary, err := checkCoverage(strings.NewReader(good))
	if err != nil {
		t.Fatal(err)
	}
	if summary["convenience.go"].all != 3 || summary["convenience.go"].covered != 3 || summary["input/keysend.go"].all != 3 || summary["input/keysend.go"].covered != 3 {
		t.Fatalf("incorrect coverage: %+v", summary)
	}
}

func TestCheckCoverageFailsOnUncoveredAndMissingFiles(t *testing.T) {
	const header = "mode: set\n"
	const covered = modulePrefix + "convenience.go:1.1,2.1 2 1\n"
	const coveredWindow = modulePrefix + "convenience.go:4.1,5.1 1 1\n"
	const keysend = modulePrefix + "input/keysend.go:1.1,2.1 3 1\n"
	cases := []string{
		header,
		header + covered,
		header + keysend,
		header + modulePrefix + "convenience.go:1.1,2.1 0 0\n" + keysend,
		header + modulePrefix + "convenience.go:1.1,2.1 2 0\n" + coveredWindow + keysend,
		header + covered + modulePrefix + "convenience.go:4.1,5.1 1 0\n" + keysend,
		header + covered + coveredWindow + modulePrefix + "input/keysend.go:1.1,2.1 3 0\n",
		header + modulePrefix + "convenience.go:1.1,2.1 abc 1\n" + keysend,
		header + modulePrefix + "convenience.go:1.1,2.1 1 x\n" + keysend,
		header + modulePrefix + "convenience.go:1.1,2.1 1\n" + keysend,
		header + modulePrefix + "other/convenience.go:1.1,2.1 3 1\n" + keysend,
		header + covered + modulePrefix + "other/keysend.go:1.1,2.1 3 1\n",
		header + "github.com/example/repo/convenience.go:1.1,2.1 3 1\n" + keysend,
	}
	for _, content := range cases {
		if _, err := checkCoverage(strings.NewReader(content)); err == nil {
			t.Fatalf("invalid coverage accepted: %q", content)
		}
	}
}
