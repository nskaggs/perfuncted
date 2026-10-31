//go:build linux

package screen

import (
	"context"
	"errors"
	"image"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/nskaggs/perfuncted/internal/x11test"
)

func TestX11HeadlessServerScreenCaptureAndCanonicalRegionHashes(t *testing.T) {
	auth := filepath.Join(t.TempDir(), "no-host-auth")
	if err := os.WriteFile(auth, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XAUTHORITY", auth)
	display, stop, err := x11test.StartXvfb()
	if err != nil {
		t.Skipf("isolated Xvfb unavailable: %v", err)
	}
	t.Cleanup(stop)
	b, err := NewX11Backend(display)
	if err != nil {
		t.Fatalf("open headless screen: %v", err)
	}
	defer b.Close()
	ctx := context.Background()
	w, h, err := b.Resolution()
	if err != nil || w != 1024 || h != 768 {
		t.Fatalf("private framebuffer=%dx%d %v", w, h, err)
	}
	rect := image.Rect(3, 5, 11, 13)
	img, err := b.Grab(ctx, rect)
	if err != nil || img == nil || img.Bounds().Dx() != 8 || img.Bounds().Dy() != 8 {
		t.Fatalf("real region=%v %v", img, err)
	}
	hash, err := b.GrabRegionHash(ctx, rect)
	if err != nil || hash == 0 {
		t.Fatalf("region hash %08x %v", hash, err)
	}
	whole, err := b.GrabFullHash(ctx)
	if err != nil || whole == 0 || whole == hash {
		t.Fatalf("full hash %08x ROI %08x %v", whole, hash, err)
	}
	full, err := b.Grab(ctx, image.Rectangle{})
	if err != nil || full.Bounds() != image.Rect(0, 0, 1024, 768) {
		t.Fatalf("full capture=%v %v", full.Bounds(), err)
	}
	if _, err := b.Grab(ctx, image.Rect(32769, 0, 32770, 1)); err == nil {
		t.Fatal("unrepresentable region accepted")
	}
	if _, err := b.Grab(ctx, image.Rect(2000, 2000, 2008, 2008)); err == nil {
		t.Fatal("out-of-screen region accepted")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.Grab(canceled, rect); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled capture: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Grab(ctx, rect); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed backend captured: %v", err)
	}
}
