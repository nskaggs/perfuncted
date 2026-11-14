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

type closeSessionDuringPNGEncoding struct {
	onRead func()
	once   sync.Once
}

func (*closeSessionDuringPNGEncoding) ColorModel() color.Model { return color.RGBAModel }
func (*closeSessionDuringPNGEncoding) Bounds() image.Rectangle { return image.Rect(0, 0, 8, 8) }
func (i *closeSessionDuringPNGEncoding) At(int, int) color.Color {
	i.once.Do(i.onRead)
	return color.RGBA{R: 0x12, G: 0x23, B: 0x34, A: 0xff}
}

// The PNG writer can continue pulling pixels after the session has been
// closed. A successfully encoded image from that revoked session must not
// replace the existing destination or leave a temporary file behind.
func TestCaptureRegionDoesNotPublishAfterSessionClosesDuringEncoding(t *testing.T) {
	img := &closeSessionDuringPNGEncoding{}
	session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: img}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	img.onRead = func() {
		if err := session.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "screen.png")
	const original = "retain previous valid screenshot"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	err := session.Screen.CaptureRegion(context.Background(), img.Bounds(), path)
	if !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("revoked encoding incorrectly published: %v", err)
	}
	saved, readErr := os.ReadFile(path)
	if readErr != nil || string(saved) != original {
		t.Fatalf("revoked capture replaced prior image: %q %v", saved, readErr)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil || len(entries) != 1 || entries[0].Name() != "screen.png" {
		t.Fatalf("revoked capture retained temporary files: %v %v", entries, readErr)
	}
}
