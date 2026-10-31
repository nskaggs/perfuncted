package main

import "testing"

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
