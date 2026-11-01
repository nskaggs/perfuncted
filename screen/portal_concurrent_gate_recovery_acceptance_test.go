//go:build linux

package screen

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The consent-gated screenshot backend must not start another portal request
// while the first one owns its capture slot. A canceled waiting caller cannot
// poison the gate, and a timed-out owner cannot strand subsequent captures.
func TestSyntheticScreenshotPortalConcurrentGateCancellationAndRecovery(t *testing.T) {
	backend, portal := newSyntheticScreenshotPortal(t)
	backend.SetCaptureTimeout(120 * time.Millisecond)
	portal.mu.Lock()
	portal.skip = true
	portal.mu.Unlock()
	firstDone := make(chan error, 1)
	go func() { _, err := backend.Grab(context.Background(), image.Rectangle{}); firstDone <- err }()
	gateDeadline := time.After(time.Second)
	for len(backend.captureGate) != 1 {
		select {
		case <-gateDeadline:
			t.Fatal("first screenshot never acquired capture slot")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	img, err := backend.Grab(ctx, image.Rectangle{})
	if img != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked second screenshot acknowledged: image=%v err=%v", img, err)
	}
	select {
	case err := <-firstDone:
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("unanswered first capture not bounded: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first capture did not release slot on timeout")
	}
	if len(backend.captureGate) != 0 {
		t.Fatalf("capture slot leaked after timeout")
	}
	filePath := filepath.Join(t.TempDir(), "recovered.png")
	f, err := os.Create(filePath)
	if err != nil {
		t.Fatal(err)
	}
	pngData := image.NewRGBA(image.Rect(0, 0, 2, 2))
	pngData.SetRGBA(1, 1, color.RGBA{R: 200, A: 255})
	if err := png.Encode(f, pngData); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	portal.mu.Lock()
	portal.skip = false
	portal.uri = "file://" + filePath
	portal.mu.Unlock()
	restored, err := backend.Grab(context.Background(), image.Rect(1, 1, 2, 2))
	if err != nil || restored == nil || restored.Bounds().Dx() != 1 || restored.Bounds().Dy() != 1 {
		t.Fatalf("post-cancellation portal did not recover: img=%v err=%v", restored, err)
	}
	if len(backend.captureGate) != 0 {
		t.Fatal("successful request did not release capture slot")
	}
}
