//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"image"
	"testing"
	"time"
)

// BenchmarkManagedScreenCapture measures capture only, with no diff or encoding.
func BenchmarkManagedScreenCapture(b *testing.B) {
	s := currentSuite
	if s.mode != displayHeadlessWayland {
		b.Skip("requires managed headless Wayland")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	w, h, err := s.pf.Screen.Resolution(ctx)
	if err != nil || w <= 0 || h <= 0 {
		b.Fatalf("resolution %dx%d: %v", w, h, err)
	}
	rect := image.Rect(0, 0, w, h)
	b.Run(fmt.Sprintf("managed-headless-wayland/%dx%d", w, h), func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			capture, err := s.pf.Screen.Capture(ctx, rect)
			if err != nil {
				b.Fatal(err)
			}
			if capture.Image == nil || capture.Image.Bounds().Size() != rect.Size() {
				b.Fatal("unexpected capture dimensions")
			}
		}
	})
}
