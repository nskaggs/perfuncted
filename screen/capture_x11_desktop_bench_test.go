//go:build desktopbench && linux

package screen

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/capturebench"
	"github.com/nskaggs/perfuncted/internal/x11test"
)

const captureBenchmarkReal = "PERFUNCTED_CAPTURE_BENCH_REAL"

// BenchmarkCaptureX11 measures direct Screenshotter.Grab calls against a
// private Xvfb server. It never uses the caller's DISPLAY value.
func BenchmarkCaptureX11(b *testing.B) {
	if os.Getenv(captureBenchmarkReal) != "1" {
		b.Skipf("real capture requires %s=1; no desktop capture attempted", captureBenchmarkReal)
	}
	b.Run("BackendOpen", func(b *testing.B) {
		b.StopTimer()
		display := startCaptureXvfb(b)
		b.ReportAllocs()
		b.ResetTimer()
		b.StartTimer()
		loggedBackend := false
		for b.Loop() {
			backend, err := NewX11Backend(display)
			b.StopTimer()
			if err != nil {
				b.Fatalf("open private X11 backend: %v", err)
			}
			if !loggedBackend {
				b.Logf("source=real-headless mode=BackendOpen backend=%T protocol=X11 output=1024x768 environment=%s", backend, captureEnvironment())
				loggedBackend = true
			}
			if err := backend.Close(); err != nil {
				b.Fatal(err)
			}
			b.StartTimer()
		}
	})
	b.Run("FirstFull", func(b *testing.B) {
		b.StopTimer()
		display := startCaptureXvfb(b)
		b.ReportAllocs()
		b.ResetTimer()
		b.StartTimer()
		loggedBackend := false
		for b.Loop() {
			b.StopTimer()
			backend, err := NewX11Backend(display)
			if err != nil {
				b.Fatalf("open private X11 backend: %v", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			b.StartTimer()
			img, err := backend.Grab(ctx, image.Rectangle{})
			captureErr := ctx.Err()
			b.StopTimer()
			cancel()
			if err == nil && captureErr != nil {
				err = captureErr
			}
			if err == nil {
				err = validCaptureImage(img)
			}
			closeErr := backend.Close()
			if err != nil {
				b.Fatalf("first full capture: %v", err)
			}
			if closeErr != nil {
				b.Fatal(closeErr)
			}
			if !loggedBackend {
				b.Logf("source=real-headless mode=FirstFull backend=%T protocol=X11 requested=%v returned=%v output=1024x768 environment=%s", backend, image.Rectangle{}, img.Bounds(), captureEnvironment())
				loggedBackend = true
			}
			b.StartTimer()
		}
	})
	b.Run("OpenAndFirstFull", func(b *testing.B) {
		b.StopTimer()
		display := startCaptureXvfb(b)
		b.ReportAllocs()
		b.ResetTimer()
		b.StartTimer()
		loggedBackend := false
		for b.Loop() {
			backend, err := NewX11Backend(display)
			if err != nil {
				b.StopTimer()
				b.Fatalf("open private X11 backend: %v", err)
			}
			b.StopTimer()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			b.StartTimer()
			img, err := backend.Grab(ctx, image.Rectangle{})
			captureErr := ctx.Err()
			b.StopTimer()
			cancel()
			if err == nil && captureErr != nil {
				err = captureErr
			}
			if err == nil {
				err = validCaptureImage(img)
			}
			closeErr := backend.Close()
			if err != nil {
				b.Fatalf("first full capture: %v", err)
			}
			if closeErr != nil {
				b.Fatal(closeErr)
			}
			if !loggedBackend {
				b.Logf("source=real-headless mode=OpenAndFirstFull backend=%T protocol=X11 requested=%v returned=%v output=1024x768 environment=%s", backend, image.Rectangle{}, img.Bounds(), captureEnvironment())
				loggedBackend = true
			}
			b.StartTimer()
		}
	})
	for _, variant := range []struct {
		name string
		rect image.Rectangle
	}{
		{name: "WarmFull"},
		{name: "WarmRegion256", rect: image.Rect(0, 0, 256, 256)},
		{name: "WarmRegionLarge", rect: image.Rect(0, 0, 1024, 720)},
	} {
		b.Run(variant.name, func(b *testing.B) {
			display := startCaptureXvfb(b)
			backend, err := NewX11Backend(display)
			if err != nil {
				b.Fatalf("open private X11 backend: %v", err)
			}
			b.Cleanup(func() {
				if err := backend.Close(); err != nil {
					b.Error(err)
				}
			})
			w, h, err := backend.Resolution()
			if err != nil {
				b.Fatal(err)
			}
			ctx := context.Background()
			if variant.rect != (image.Rectangle{}) && (variant.rect.Max.X > w || variant.rect.Max.Y > h) {
				b.Skipf("region %v exceeds output geometry %dx%d", variant.rect, w, h)
			}
			if _, _, err := capturebench.Collect(ctx, backend, variant.rect, 1, time.Second); err != nil {
				b.Fatalf("warmup capture: %v", err)
			}
			warmup, bounds, err := capturebench.Collect(ctx, backend, variant.rect, 50, time.Second)
			if err != nil {
				b.Fatalf("warmup capture: %v", err)
			}
			d, err := capturebench.Summarize(warmup)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.Logf("source=real-headless mode=%s backend=%T protocol=X11 requested=%v returned=%v output=%dx%d environment=%s", variant.name, backend, variant.rect, bounds, w, h, captureEnvironment())
			b.ResetTimer()
			for b.Loop() {
				b.StopTimer()
				captureCtx, cancel := context.WithTimeout(ctx, time.Second)
				b.StartTimer()
				img, err := backend.Grab(captureCtx, variant.rect)
				captureErr := captureCtx.Err()
				b.StopTimer()
				cancel()
				if err == nil && captureErr != nil {
					err = captureErr
				}
				if err != nil {
					b.Fatal(err)
				}
				if err := validCaptureImage(img); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
			}
			reportCaptureDistribution(b, d)
		})
	}
}

func startCaptureXvfb(b *testing.B) string {
	b.Helper()
	privateAuthority := filepath.Join(b.TempDir(), "empty-xauthority")
	if err := os.WriteFile(privateAuthority, nil, 0600); err != nil {
		b.Fatalf("create private empty X authority file: %v", err)
	}
	b.Setenv("XAUTHORITY", privateAuthority)
	display, stop, err := x11test.StartXvfbNoReset()
	if err != nil {
		b.Skipf("private Xvfb unavailable: %v", err)
	}
	b.Cleanup(stop)
	return display
}

func validCaptureImage(img image.Image) error {
	if img == nil {
		return errors.New("capture benchmark: nil image")
	}
	if img.Bounds().Empty() {
		return errors.New("capture benchmark: empty image")
	}
	return nil
}
