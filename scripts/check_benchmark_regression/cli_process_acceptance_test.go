package main

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The real CLI uses os.Exit for argument errors and measured regressions.
// Execute main inside a fresh copy of the test process to cover those exits,
// including status and diagnostics, without mutating the running test suite.
func TestBenchmarkCheckerCLISubprocess(t *testing.T) {
	if os.Getenv("PF_BENCH_CHECK_CHILD") == "1" {
		args := strings.Split(os.Getenv("PF_BENCH_CHECK_ARGS"), "\x1f")
		flag.CommandLine = flag.NewFlagSet("benchmark-regression", flag.ContinueOnError)
		os.Args = append([]string{"benchmark-regression"}, args...)
		main()
		return
	}
	const baseline = "BenchmarkDesktopWindow-8 10 100 ns/op 20 B/op 2 allocs/op\n"
	for _, tc := range []struct {
		name, old, new, threshold string
		missingOld, missingNew    bool
		wantCode                  int
		wantOutput                string
	}{
		{name: "within threshold", old: baseline, new: "BenchmarkDesktopWindow-8 10 140 ns/op 24 B/op 2 allocs/op\n", threshold: "0.50", wantCode: 0, wantOutput: "BenchmarkDesktopWindow (best):"},
		{name: "runtime regression", old: baseline, new: "BenchmarkDesktopWindow-8 10 200 ns/op 20 B/op 2 allocs/op\n", threshold: "0.5", wantCode: 1, wantOutput: "ns/op regressed"},
		{name: "memory regression", old: baseline, new: "BenchmarkDesktopWindow-8 10 100 ns/op 50 B/op 2 allocs/op\n", threshold: "0.5", wantCode: 1, wantOutput: "B/op regressed"},
		{name: "allocations regression", old: baseline, new: "BenchmarkDesktopWindow-8 10 100 ns/op 20 B/op 6 allocs/op\n", threshold: "0.5", wantCode: 1, wantOutput: "allocs/op regressed"},
		{name: "new benchmark missing", old: baseline, new: "BenchmarkDesktopOther-8 10 100 ns/op 20 B/op 2 allocs/op\n", threshold: "0.5", wantCode: 1, wantOutput: "missing from current output"},
		{name: "invalid current", old: baseline, new: "BenchmarkDesktopWindow-8 10 +Inf ns/op 20 B/op 2 allocs/op\n", threshold: "0.5", wantCode: 1, wantOutput: "no completed benchmarks"},
		{name: "invalid baseline", old: "invalid data", new: baseline, threshold: "0.5", wantCode: 1, wantOutput: "no completed benchmarks"},
		{name: "invalid threshold", old: baseline, new: baseline, threshold: "NaN", wantCode: 2, wantOutput: "finite and non-negative"},
		{name: "negative threshold", old: baseline, new: baseline, threshold: "-0.1", wantCode: 2, wantOutput: "finite and non-negative"},
		{name: "unreadable baseline", new: baseline, threshold: "0.5", missingOld: true, wantCode: 1, wantOutput: "read "},
		{name: "unreadable current", old: baseline, threshold: "0.5", missingNew: true, wantCode: 1, wantOutput: "read "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			old := filepath.Join(dir, "baseline.txt")
			current := filepath.Join(dir, "current.txt")
			if !tc.missingOld {
				if err := os.WriteFile(old, []byte(tc.old), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if !tc.missingNew {
				if err := os.WriteFile(current, []byte(tc.new), 0600); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"-baseline", old, "-current", current, "-max-regression", tc.threshold}
			child := exec.Command(os.Args[0], "-test.run=^TestBenchmarkCheckerCLISubprocess$")
			child.Env = append(os.Environ(), "PF_BENCH_CHECK_CHILD=1", "PF_BENCH_CHECK_ARGS="+strings.Join(args, "\x1f"))
			var stdout, stderr bytes.Buffer
			child.Stdout = &stdout
			child.Stderr = &stderr
			err := child.Run()
			code := 0
			if err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatalf("child process: %v", err)
				}
				code = exit.ExitCode()
			}
			if code != tc.wantCode {
				t.Fatalf("exit %d, wanted %d; stdout=%q stderr=%q", code, tc.wantCode, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String()+stderr.String(), tc.wantOutput) {
				t.Fatalf("missing %q in stdout=%q stderr=%q", tc.wantOutput, stdout.String(), stderr.String())
			}
		})
	}
}

// Normal successful invocations are exercised in-process as well: unlike the
// external exit-code tests, this measures the production CLI loop in the
// repository-wide Go statement coverage profile.
func TestBenchmarkCheckerMainSuccessInProcess(t *testing.T) {
	dir := t.TempDir()
	baseline := filepath.Join(dir, "before.txt")
	current := filepath.Join(dir, "after.txt")
	before := "BenchmarkDesktopWindow-8 10 130 ns/op 18 B/op 2 allocs/op\nBenchmarkDesktopWindow-8 10 110 ns/op 22 B/op 3 allocs/op\nBenchmarkDesktopOpenClose-8 10 90 ns/op 10 B/op 1 allocs/op\n"
	after := "BenchmarkDesktopWindow-8 10 115 ns/op 18 B/op 3 allocs/op\nBenchmarkDesktopWindow-8 10 200 ns/op 90 B/op 8 allocs/op\nBenchmarkDesktopOpenClose-8 10 100 ns/op 10 B/op 1 allocs/op\n"
	if err := os.WriteFile(baseline, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, []byte(after), 0600); err != nil {
		t.Fatal(err)
	}
	originalFlags := flag.CommandLine
	originalArgs := os.Args
	t.Cleanup(func() { flag.CommandLine = originalFlags; os.Args = originalArgs })
	flag.CommandLine = flag.NewFlagSet("benchmark-regression", flag.ContinueOnError)
	os.Args = []string{"benchmark-regression", "-baseline", baseline, "-current", current, "-max-regression", "0.15"}
	main()
}
