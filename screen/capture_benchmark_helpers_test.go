package screen

import (
	"fmt"
	"image"
	"image/png"
	"io"
	"runtime"
	"testing"

	"github.com/nskaggs/perfuncted/internal/capturebench"
)

func reportCaptureDistribution(b *testing.B, d capturebench.Distribution) {
	b.Helper()
	b.ReportMetric(float64(d.Min.Nanoseconds()), "min-ns")
	b.ReportMetric(float64(d.P50.Nanoseconds()), "p50-ns")
	if d.HasP95 {
		b.ReportMetric(float64(d.P95.Nanoseconds()), "p95-ns")
	}
	b.ReportMetric(float64(d.Max.Nanoseconds()), "max-ns")
	b.ReportMetric(float64(d.Count), "samples")
}

func captureEnvironment() string {
	return fmt.Sprintf("go=%s os=%s/%s cpu=%d", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
}

// BenchmarkCapturePNGEncode measures optional caller work on an in-memory image
// fixture. It is not a screenshot latency benchmark.
func BenchmarkCapturePNGEncode(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 1024, 768))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := png.Encode(io.Discard, img); err != nil {
			b.Fatal(err)
		}
	}
}
