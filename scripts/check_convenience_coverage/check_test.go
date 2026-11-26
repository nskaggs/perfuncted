package coveragecheck

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
)

const modulePrefix = "github.com/nskaggs/perfuncted/v2/"

var requiredFiles = []string{"convenience.go", "input/keysend.go"}

type totals struct {
	all     int
	covered int
}

// checkCoverage checks real Go coverprofile blocks, ignoring empty blocks and
// legacy code outside the new scripting layer.
func checkCoverage(r io.Reader) (map[string]totals, error) {
	results := make(map[string]totals, len(requiredFiles))
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		var target string
		for _, name := range requiredFiles {
			if strings.HasPrefix(line, modulePrefix+name+":") {
				target = name
				break
			}
		}
		if target == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil, fmt.Errorf("invalid coverage entry: %q", line)
		}
		count, err := strconv.Atoi(fields[1])
		if err != nil || count < 0 {
			return nil, fmt.Errorf("invalid statement count in %q", line)
		}
		hits, err := strconv.ParseUint(fields[2], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid execution count in %q: %w", line, err)
		}
		total := results[target]
		total.all += count
		if hits > 0 {
			total.covered += count
		} else if count != 0 {
			fmt.Fprintf(os.Stderr, "uncovered %s\n", fields[0])
		}
		results[target] = total
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	for _, name := range requiredFiles {
		result, ok := results[name]
		if !ok || result.all == 0 {
			return nil, fmt.Errorf("coverage profile lacks statements for %s", name)
		}
		if result.all != result.covered {
			return results, fmt.Errorf("%s: %d of %d statements covered, require 100%%", name, result.covered, result.all)
		}
	}
	return results, nil
}

// TestCoverageGate enforces 100% when invoked with an actual profile argument
// by just coverage-convenience; ordinary package tests run its parser tests.
func TestCoverageGate(t *testing.T) {
	if flag.NArg() == 0 {
		t.Skip("pass Go coverprofile through -args to enforce coverage")
	}
	file, err := os.Open(flag.Arg(0))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	results, err := checkCoverage(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range requiredFiles {
		result := results[name]
		t.Logf("%s: %d/%d statements (100%%)", name, result.covered, result.all)
	}
}
