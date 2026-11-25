// check_release_command_contracts executes every contract scenario named in
// the command ledger, failing on absent or skipped tests as well as failures.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func requiredContracts(data string) (map[string]bool, error) {
	required := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		_, evidence, ok := strings.Cut(line, "\t")
		if !ok {
			return nil, fmt.Errorf("malformed ledger: %q", line)
		}
		tier, refs, _ := strings.Cut(evidence, ":")
		if tier == "contract" {
			for _, name := range strings.Split(refs, "/") {
				required[name] = false
			}
		}
	}
	if len(required) == 0 {
		return nil, fmt.Errorf("no contract evidence found")
	}
	return required, nil
}

func run() error {
	if len(os.Args) > 1 {
		return runLive(os.Args[1:])
	}
	data, err := os.ReadFile("cmd/pf/testdata/release-command-coverage.tsv")
	if err != nil {
		return err
	}
	required, err := requiredContracts(string(data))
	if err != nil {
		return err
	}
	names := make([]string, 0, len(required))
	for name := range required {
		names = append(names, regexp.QuoteMeta(name))
	}
	slices.Sort(names)
	return executeRequired(required, []string{"-short", "./cmd/pf", "-run", "^(" + strings.Join(names, "|") + ")$"})
}

// The live lane supplies its exact required test names and package; missing
// names and skips fail even if go test itself exits successfully.
func runLive(args []string) error {
	if len(args) < 3 || args[0] != "--live" {
		return fmt.Errorf("usage: --live PACKAGE TestName ...")
	}
	required := map[string]bool{}
	names := make([]string, 0, len(args)-2)
	for _, name := range args[2:] {
		if _, exists := required[name]; !strings.HasPrefix(name, "Test") || exists {
			return fmt.Errorf("invalid required test %q", name)
		}
		required[name] = false
		names = append(names, regexp.QuoteMeta(name))
	}
	return executeRequired(required, []string{"-tags=integration", args[1], "-run", "^(" + strings.Join(names, "|") + ")$", "-timeout=10m"})
}

func executeRequired(required map[string]bool, args []string) error {
	cmd := exec.Command("go", append([]string{"test", "-json", "-count=1"}, args...)...)
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	eventErr := verifyEvents(out, os.Stdout, required)
	if err := cmd.Wait(); err != nil {
		return err
	}
	if eventErr != nil {
		return eventErr
	}
	fmt.Printf("PASS: all %d named acceptance tests executed without skips\n", len(required))
	return nil
}

func verifyEvents(in io.Reader, out io.Writer, required map[string]bool) error {
	scan := bufio.NewScanner(in)
	scan.Buffer(make([]byte, 4096), 4*1024*1024)
	var eventErr error
	for scan.Scan() {
		var event struct{ Action, Test, Output string }
		if err := json.Unmarshal(scan.Bytes(), &event); err != nil {
			eventErr = err
			continue
		}
		if event.Output != "" {
			if _, err := fmt.Fprint(out, event.Output); err != nil {
				eventErr = err
			}
		}
		if _, ok := required[event.Test]; ok && event.Action == "pass" {
			required[event.Test] = true
		}
		if event.Action == "skip" && event.Test != "" {
			eventErr = fmt.Errorf("required contract or subtest skipped: %s", event.Test)
		}
		if event.Action == "fail" {
			eventErr = fmt.Errorf("contract test failed: %s", event.Test)
		}
	}
	if err := scan.Err(); err != nil {
		return err
	}
	if eventErr != nil {
		return eventErr
	}
	for name, passed := range required {
		if !passed {
			return fmt.Errorf("required contract did not pass: %s", name)
		}
	}
	return nil
}
