//go:build linux

package screen

import (
	"context"
	"image"
	"testing"
	"time"
)

// A closed screenshot transport is no longer authorized to perform captures,
// even when its provider still owns the well-known portal service name.
func TestPortalCaptureRejectsClosedClientTransportWithoutPixelsOrGateLeak(t *testing.T) {
	backend, provider := newSyntheticScreenshotPortal(t)
	if err := backend.Close(); err != nil {
		t.Fatalf("close portal transport: %v", err)
	}
	if !provider.conn.Connected() {
		t.Fatal("closing client also disconnected portal provider")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	img, err := backend.Grab(ctx, image.Rectangle{})
	if err == nil || img != nil {
		t.Fatalf("capture after client transport close: image=%v err=%v", img, err)
	}
	if len(backend.captureGate) != 0 {
		t.Fatal("closed portal capture leaked admission gate")
	}
	if !provider.conn.Connected() {
		t.Fatal("failed client capture disrupted independent portal provider")
	}
}
