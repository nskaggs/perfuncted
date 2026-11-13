package perfuncted

import (
	"context"
	"errors"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type cancelDuringPNGEncoding struct {
	cancel context.CancelFunc
	once   sync.Once
}

func (i *cancelDuringPNGEncoding) ColorModel() color.Model { return color.RGBAModel }
func (i *cancelDuringPNGEncoding) Bounds() image.Rectangle { return image.Rect(0, 0, 8, 8) }
func (i *cancelDuringPNGEncoding) At(x, y int) color.Color {
	i.once.Do(i.cancel)
	return color.RGBA{R: 0x12, G: 0x23, B: 0x34, A: 0xff}
}

// A screenshot may be valid when captured, but its PNG encoder can outlive
// caller authorization. Never publish an encoded file after cancellation.
func TestCaptureRegionDoesNotPublishAfterEncodingCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	img := &cancelDuringPNGEncoding{cancel: cancel}
	session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: img}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	dir := t.TempDir()
	path := filepath.Join(dir, "screen.png")
	const original = "keep previous screenshot"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	err := session.Screen.CaptureRegion(ctx, img.Bounds(), path)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("encoder cancellation acknowledged as success: %v", err)
	}
	saved, readErr := os.ReadFile(path)
	if readErr != nil || string(saved) != original {
		t.Fatalf("canceled encoding replaced old screenshot: %q %v", saved, readErr)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil || len(entries) != 1 || entries[0].Name() != "screen.png" {
		t.Fatalf("canceled encoding left staging file: %v %v", entries, readErr)
	}
}
