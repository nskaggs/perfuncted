// Command check_benchmark_regression compares bounded desktop benchmark output
// with a checked-in baseline. The deliberately broad default threshold covers
// compositor noise; a regression still fails when a measured budget is
// exceeded.
package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type sample struct {
	name       string
	nsPerOp    float64
	bytesPerOp float64
	allocsOp   float64
}

var benchmarkSuffix = regexp.MustCompile(`-[0-9]+$`)

func main() {
	baselinePath := flag.String("baseline", "", "baseline benchmark output")
	currentPath := flag.String("current", "", "current benchmark output")
	maxRegression := flag.Float64("max-regression", 0.50, "maximum allowed relative increase")
	flag.Parse()
	if *baselinePath == "" || *currentPath == "" {
		fmt.Fprintln(os.Stderr, "usage: check_benchmark_regression -baseline FILE -current FILE [-max-regression FRACTION]")
		os.Exit(2)
	}
	if *maxRegression < 0 {
		fmt.Fprintln(os.Stderr, "max-regression must be non-negative")
		os.Exit(2)
	}
	baseline, err := readSamples(*baselinePath)
	if err != nil {
		fail(err)
	}
	current, err := readSamples(*currentPath)
	if err != nil {
		fail(err)
	}
	if err := validateSamples(baseline, current); err != nil {
		fail(err)
	}
	var failures []string
	for name, oldSamples := range baseline {
		old := medianSample(oldSamples)
		newSamples, ok := current[name]
		if !ok {
			failures = append(failures, fmt.Sprintf("%s missing from current output", name))
			continue
		}
		newSample := medianSample(newSamples)
		for _, metric := range []struct {
			name string
			old  float64
			new  float64
		}{
			{name: "ns/op", old: old.nsPerOp, new: newSample.nsPerOp},
			{name: "B/op", old: old.bytesPerOp, new: newSample.bytesPerOp},
			{name: "allocs/op", old: old.allocsOp, new: newSample.allocsOp},
		} {
			if metric.old <= 0 || metric.new <= 0 || metric.new <= metric.old*(1+*maxRegression) {
				continue
			}
			failures = append(failures, fmt.Sprintf(
				"%s %s regressed from %.3f to %.3f (threshold %.0f%%)",
				name, metric.name, metric.old, metric.new, *maxRegression*100,
			))
		}
		fmt.Printf("%s (median): ns/op %.3f -> %.3f, B/op %.3f -> %.3f, allocs/op %.3f -> %.3f\n",
			name, old.nsPerOp, newSample.nsPerOp, old.bytesPerOp, newSample.bytesPerOp, old.allocsOp, newSample.allocsOp)
	}
	if len(failures) != 0 {
		for _, failure := range failures {
			fmt.Fprintln(os.Stderr, "benchmark regression:", failure)
		}
		os.Exit(1)
	}
}

func validateSamples(baseline, current map[string][]sample) error {
	if len(current) == 0 {
		return fmt.Errorf("current benchmark output contains no completed benchmarks")
	}
	if len(baseline) == 0 {
		return fmt.Errorf("baseline benchmark output contains no completed benchmarks")
	}
	return nil
}

func readSamples(path string) (map[string][]sample, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	samples := make(map[string][]sample)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.HasPrefix(fields[0], "BenchmarkDesktop") || strings.HasPrefix(fields[0], "BenchmarkDesktopAccessibility") {
			continue
		}
		parsed, ok := parseSample(fields)
		if ok {
			samples[parsed.name] = append(samples[parsed.name], parsed)
		}
	}
	return samples, nil
}

func medianSample(samples []sample) sample {
	return sample{
		name:       samples[0].name,
		nsPerOp:    medianValue(samples, func(value sample) float64 { return value.nsPerOp }),
		bytesPerOp: medianValue(samples, func(value sample) float64 { return value.bytesPerOp }),
		allocsOp:   medianValue(samples, func(value sample) float64 { return value.allocsOp }),
	}
}

func medianValue(samples []sample, value func(sample) float64) float64 {
	values := make([]float64, 0, len(samples))
	for _, sample := range samples {
		values = append(values, value(sample))
	}
	sort.Float64s(values)
	return values[len(values)/2]
}

func parseSample(fields []string) (sample, bool) {
	s := sample{name: benchmarkSuffix.ReplaceAllString(fields[0], "")}
	for i := 1; i+1 < len(fields); i++ {
		value, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			continue
		}
		switch fields[i+1] {
		case "ns/op":
			s.nsPerOp = value
		case "B/op":
			s.bytesPerOp = value
		case "allocs/op":
			s.allocsOp = value
		}
	}
	return s, s.nsPerOp > 0 && s.bytesPerOp > 0 && s.allocsOp > 0
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
