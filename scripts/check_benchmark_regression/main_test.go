package main

import (
	"bytes"
	"errors"
	"flag"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdversarialBenchmarkSamples(t *testing.T) {
	cases := []struct {
		name, body string
		want       int
	}{
		{"complete", "BenchmarkDesktopWindow-8  100  100 ns/op  32 B/op  2 allocs/op\n", 1},
		{"missing allocation", "BenchmarkDesktopWindow-8  100  100 ns/op  32 B/op\n", 0},
		{"invalid infinity", "BenchmarkDesktopWindow-8  100  +Inf ns/op  32 B/op  2 allocs/op\n", 0},
		{"negative time", "BenchmarkDesktopWindow-8  100  -12 ns/op  32 B/op  2 allocs/op\n", 0},
		{"unrelated", "BenchmarkOther-8 100 100 ns/op 32 B/op 2 allocs/op\n", 0},
		{"excluded accessibility", "BenchmarkDesktopAccessibility-8 100 100 ns/op 32 B/op 2 allocs/op\n", 0},
		{"malformed then valid", "BenchmarkDesktopWindow-8 ???\nBenchmarkDesktopWindow-8 100 75 ns/op 16 B/op 1 allocs/op\n", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "bench.txt")
			if err := os.WriteFile(p, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			samples, err := readSamples(p)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, v := range samples {
				count += len(v)
			}
			if count != tc.want {
				t.Fatalf("accepted %d samples; want %d: %v", count, tc.want, samples)
			}
		})
	}
	if _, err := readSamples(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing input accepted")
	}
}
func TestAdversarialBenchmarkSelectionAndBounds(t *testing.T) {
	samples := []sample{{name: "a", nsPerOp: 10, bytesPerOp: 100, allocsOp: 8}, {name: "a", nsPerOp: 5, bytesPerOp: 200, allocsOp: 9}}
	if got := bestSample(samples); got.nsPerOp != 5 || got.bytesPerOp != 200 || got.allocsOp != 9 {
		t.Fatalf("metrics were mixed between samples: %+v", got)
	}
	for _, v := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if validMaxRegression(v) {
			t.Fatalf("invalid threshold accepted: %v", v)
		}
	}
	for _, tc := range []struct {
		base, current map[string][]sample
		fragment      string
	}{
		{nil, map[string][]sample{"a": samples}, "baseline"},
		{map[string][]sample{"a": samples}, nil, "current"},
	} {
		err := validateSamples(tc.base, tc.current)
		if err == nil || !strings.Contains(err.Error(), tc.fragment) {
			t.Fatalf("invalid samples accepted: %v", err)
		}
	}
}

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

// Repeated metric labels must not silently overwrite one measurement with a
// different one; otherwise malformed benchmark output can bypass regression gates.
func TestParseSampleRejectsConflictingRepeatedMetric(t *testing.T) {
	for _, fields := range [][]string{
		{"BenchmarkDesktopWindow-8", "1", "100", "ns/op", "10", "B/op", "1", "allocs/op", "1", "ns/op"},
		{"BenchmarkDesktopWindow-8", "1", "100", "ns/op", "10", "B/op", "1", "allocs/op", "1", "B/op"},
		{"BenchmarkDesktopWindow-8", "1", "100", "ns/op", "10", "B/op", "1", "allocs/op", "2", "allocs/op"},
	} {
		if parsed, ok := parseSample(fields); ok {
			t.Fatalf("accepted contradictory sample: %+v", parsed)
		}
	}
}

func TestParseSampleRejectsNonFiniteMeasurements(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields []string
	}{
		{"infinite time", []string{"BenchmarkDesktopOpenClose-8", "3", "+Inf", "ns/op", "10", "B/op", "2", "allocs/op"}},
		{"infinite bytes", []string{"BenchmarkDesktopOpenClose-8", "3", "10", "ns/op", "+Inf", "B/op", "2", "allocs/op"}},
		{"infinite allocs", []string{"BenchmarkDesktopOpenClose-8", "3", "10", "ns/op", "10", "B/op", "+Inf", "allocs/op"}},
		{"negative time", []string{"BenchmarkDesktopOpenClose-8", "3", "-4", "ns/op", "10", "B/op", "2", "allocs/op"}},
		{"zero allocs", []string{"BenchmarkDesktopOpenClose-8", "3", "10", "ns/op", "10", "B/op", "0", "allocs/op"}},
		{"nan bytes", []string{"BenchmarkDesktopOpenClose-8", "3", "10", "ns/op", "NaN", "B/op", "2", "allocs/op"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := parseSample(tc.fields); ok {
				t.Fatalf("accepted invalid sample: %+v", got)
			}
		})
	}
}

func TestParseSampleNormalizesCPUAndMetricOrdering(t *testing.T) {
	parsed, ok := parseSample([]string{"BenchmarkDesktopWindowListActivate-32", "5", "7", "allocs/op", "310", "ns/op", "512", "B/op"})
	if !ok || parsed.name != "BenchmarkDesktopWindowListActivate" || parsed.nsPerOp != 310 || parsed.bytesPerOp != 512 || parsed.allocsOp != 7 {
		t.Fatalf("parsed sample = %+v, valid %v", parsed, ok)
	}
}

func TestReadSamplesFiltersAndAccumulatesCompletedRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bench.txt")
	data := strings.Join([]string{
		"goos: linux",
		"BenchmarkDesktopOpenClose-8 3 300 ns/op 10 B/op 2 allocs/op",
		"BenchmarkDesktopAccessibilitySnapshot-8 3 999 ns/op 11 B/op 2 allocs/op",
		"BenchmarkDesktopOpenClose-8 3 200 ns/op 12 B/op 3 allocs/op",
		"BenchmarkDesktopOpenClose-8 3 +Inf ns/op 10 B/op 2 allocs/op",
		"BenchmarkOther-8 3 100 ns/op 10 B/op 2 allocs/op",
		"BenchmarkDesktopMalformed-8 3 50 ns/op",
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	samples, err := readSamples(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 1 || len(samples["BenchmarkDesktopOpenClose"]) != 2 {
		t.Fatalf("unexpected parsed samples: %+v", samples)
	}
	best := bestSample(samples["BenchmarkDesktopOpenClose"])
	if best.nsPerOp != 200 || best.bytesPerOp != 12 || best.allocsOp != 3 {
		t.Fatalf("best sample mixes runs or is not fastest: %+v", best)
	}
}

func TestReadSamplesMissingFile(t *testing.T) {
	if _, err := readSamples(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing benchmark file should be rejected")
	}
}

func TestValidateSamplesRequiresBothSides(t *testing.T) {
	existing := map[string][]sample{"BenchmarkDesktopOpenClose": {{name: "BenchmarkDesktopOpenClose", nsPerOp: 1, bytesPerOp: 1, allocsOp: 1}}}
	if err := validateSamples(existing, nil); err == nil {
		t.Fatal("empty current result should fail")
	}
	if err := validateSamples(nil, existing); err == nil {
		t.Fatal("empty baseline result should fail")
	}
	if err := validateSamples(existing, existing); err != nil {
		t.Fatalf("valid samples rejected: %v", err)
	}
}

func TestMaxRegressionBudgetRejectsNonFiniteAndNegativeLimits(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -0.1} {
		if validMaxRegression(value) {
			t.Fatalf("accepted unsafe benchmark regression budget %v", value)
		}
	}
	for _, value := range []float64{0, 0.5, 2} {
		if !validMaxRegression(value) {
			t.Fatalf("rejected valid budget %v", value)
		}
	}
}
