package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
