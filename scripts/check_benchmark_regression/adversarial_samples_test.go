package main

import (
	"math"
	"os"
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
