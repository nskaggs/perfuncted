//go:build linux

package screen

import (
	"context"
	"errors"
	"image"
	"strings"
	"testing"
	"time"
)

// A policy shorter than portal transport startup must never authorize pixels,
// even when a real, connected private D-Bus screenshot provider is available.
func TestPortalImmediateCapturePolicyExpirationNeverPublishesPixels(t *testing.T) {
	backend, _ := newSyntheticScreenshotPortal(t)
	backend.SetCaptureTimeout(time.Nanosecond)

	for i := 0; i < 5; i++ {
		img, err := backend.Grab(context.Background(), image.Rectangle{})
		if img != nil {
			t.Fatalf("request %d published pixels after expiry: %v", i, img.Bounds())
		}
		if !errors.Is(err, context.DeadlineExceeded) && (err == nil || !strings.Contains(err.Error(), "timed out waiting for screenshot")) {
			t.Fatalf("request %d error = %v, want policy deadline", i, err)
		}
		if len(backend.captureGate) != 0 {
			t.Fatalf("request %d leaked screenshot admission gate", i)
		}
	}
}
