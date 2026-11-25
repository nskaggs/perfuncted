//go:build linux

package screen

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/internal/gnomebridge"
	"image"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The GNOME bridge is an external source of geometry. Reject malformed
// metadata before rendering or hashing even when the PNG itself decodes.
func TestNormalizeGnomeCaptureRejectsMalformedGeometryMatrix(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 2, 2))
	valid := gnomebridge.ScreenCapture{Width: 2, Height: 2, PixelWidth: 2, PixelHeight: 2, Scale: 1}
	tests := []struct {
		name   string
		change func(*gnomebridge.ScreenCapture)
	}{
		{"zero logical width", func(c *gnomebridge.ScreenCapture) { c.Width = 0 }},
		{"negative logical height", func(c *gnomebridge.ScreenCapture) { c.Height = -1 }},
		{"zero pixel width", func(c *gnomebridge.ScreenCapture) { c.PixelWidth = 0 }},
		{"negative pixel height", func(c *gnomebridge.ScreenCapture) { c.PixelHeight = -1 }},
		{"zero scale", func(c *gnomebridge.ScreenCapture) { c.Scale = 0 }},
		{"negative scale", func(c *gnomebridge.ScreenCapture) { c.Scale = -1 }},
		{"not a number scale", func(c *gnomebridge.ScreenCapture) { c.Scale = math.NaN() }},
		{"positive infinite scale", func(c *gnomebridge.ScreenCapture) { c.Scale = math.Inf(1) }},
		{"negative infinite scale", func(c *gnomebridge.ScreenCapture) { c.Scale = math.Inf(-1) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			capture := valid
			tc.change(&capture)
			got, err := normalizeGnomeCapture(frame, capture)
			if got != nil || err == nil || !strings.Contains(err.Error(), "invalid capture geometry") {
				t.Fatalf("malformed capture returned image=%v error=%v", got, err)
			}
		})
	}
}

// Identity geometry must not resample or replace the image, preserving its
// origin and color model for consumers of an unscaled GNOME screenshot.
func TestNormalizeGnomeCaptureIdentityPreservesFrame(t *testing.T) {
	frame := image.NewRGBA(image.Rect(7, 9, 9, 11))
	capture := gnomebridge.ScreenCapture{Width: 2, Height: 2, PixelWidth: 2, PixelHeight: 2, Scale: 1}
	got, err := normalizeGnomeCapture(frame, capture)
	if err != nil {
		t.Fatal(err)
	}
	if got != frame || got.Bounds() != frame.Bounds() {
		t.Fatalf("identity capture was resampled: image=%T bounds=%v", got, got.Bounds())
	}
}

func TestNormalizeGnomeCaptureRejectsTypedNilFrame(t *testing.T) {
	var rgba *image.RGBA
	capture := gnomebridge.ScreenCapture{Width: 2, Height: 2, PixelWidth: 2, PixelHeight: 2, Scale: 1}
	got, err := normalizeGnomeCapture(rgba, capture)
	if err == nil || got != nil {
		t.Fatalf("invalid GNOME image accepted: %v, %v", got, err)
	}
}

// Both GNOME screenshot paths use validateGnomeCaptureRect before
// choosing full-screen capture. Only an all-zero rectangle may mean full.
func TestGnomeCaptureRejectsNonzeroEmptyRegions(t *testing.T) {
	for _, rect := range []image.Rectangle{
		image.Rect(1, 1, 1, 1),
		{Min: image.Pt(8, 8), Max: image.Pt(2, 2)},
		image.Rect(-3, 4, 7, 4),
	} {
		t.Run(rect.String(), func(t *testing.T) {
			err := validateGnomeCaptureRect(rect)
			if err == nil || !strings.Contains(err.Error(), "invalid empty capture region") {
				t.Errorf("validateGnomeCaptureRect(%v) err=%v; want invalid region", rect, err)
			}
		})
	}
	if err := validateGnomeCaptureRect(image.Rectangle{}); err != nil {
		t.Fatalf("zero rectangle should remain full-screen sentinel: %v", err)
	}
}

// The hash and resolution adapters must never convert a failed GNOME capture
// into apparently valid black pixels, a zero checksum, or screen dimensions.
func TestGnomeShellHashAndResolutionPropagateRevokedAuthorization(t *testing.T) {
	backend, provider := newSyntheticGnomeScreenshotBackend(t)
	provider.mu.Lock()
	provider.deny = true
	provider.mu.Unlock()
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		hash func() (uint32, error)
	}{
		{"full", func() (uint32, error) { return backend.GrabFullHash(ctx) }},
		{"region", func() (uint32, error) { return backend.GrabRegionHash(ctx, image.Rect(1, 1, 3, 3)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sum, err := tc.hash()
			if sum != 0 || err == nil || !strings.Contains(err.Error(), "permission denied") {
				t.Fatalf("revoked capture returned hash=%x err=%v", sum, err)
			}
		})
	}
	w, h, err := backend.Resolution()
	if w != 0 || h != 0 || err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("revoked resolution returned %dx%d err=%v", w, h, err)
	}
}

func TestGnomeShellCaptureRejectsUnavailableAndCanceledBackend(t *testing.T) {
	var backend *GnomeShellScreenshotBackend
	for _, tc := range []struct {
		name string
		f    func() error
	}{
		{"nil grab", func() error { _, err := backend.Grab(context.Background(), image.Rectangle{}); return err }},
		{"nil full hash", func() error { _, err := backend.GrabFullHash(context.Background()); return err }},
		{"nil region hash", func() error {
			_, err := backend.GrabRegionHash(context.Background(), image.Rect(0, 0, 1, 1))
			return err
		}},
		{"nil resolution", func() error { _, _, err := backend.Resolution(); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.f(); err == nil || !strings.Contains(err.Error(), "not initialized") {
				t.Fatalf("nil backend success: %v", err)
			}
		})
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("nil backend close: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewGnomeShellScreenshotBackendForBusContext(ctx, "invalid-bus"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled constructor reached DBus: %v", err)
	}
}

// A compositor-side replacement of the screenshot transport path must not
// turn the screenshot decoder into a reader of unrelated local files.
func TestGnomeScreenshotDecoderRejectsSymlinkOutsideTransportRoot(t *testing.T) {
	transportRoot := t.TempDir()
	unrelatedRoot := t.TempDir()
	secret := filepath.Join(unrelatedRoot, "private-document.png")
	if err := os.WriteFile(secret, []byte("not a screenshot"), 0600); err != nil {
		t.Fatal(err)
	}
	transport := filepath.Join(transportRoot, "transport.png")
	if err := os.Symlink(secret, transport); err != nil {
		t.Fatal(err)
	}
	img, err := decodeScreenshot(transport)
	if img != nil || err == nil || !strings.Contains(err.Error(), "path escapes") {
		t.Fatalf("decoder read outside transport root: img=%v error=%v", img, err)
	}
	b, err := os.ReadFile(secret)
	if err != nil || string(b) != "not a screenshot" {
		t.Fatalf("unrelated file changed: %q %v", b, err)
	}
}

// A capture cannot proceed without a private transport file. Failure to
// create it must not send a screenshot request to the compositor.
func TestGnomeShellCaptureRejectsUnwritableTemporaryDirectoryWithoutDBus(t *testing.T) {
	backend, service := newSyntheticGnomeScreenshotBackend(t)
	service.mu.Lock()
	previous := len(service.observed)
	service.mu.Unlock()
	invalid := filepath.Join(t.TempDir(), "missing", "transport")
	t.Setenv("TMPDIR", invalid)
	for _, tc := range []struct {
		name    string
		capture func() (image.Image, error)
	}{
		{"full", func() (image.Image, error) { return backend.Grab(context.Background(), image.Rectangle{}) }},
		{"area", func() (image.Image, error) { return backend.Grab(context.Background(), image.Rect(0, 0, 5, 5)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img, err := tc.capture()
			if img != nil || err == nil || !strings.Contains(err.Error(), "temp file") {
				t.Fatalf("capture with inaccessible TMPDIR image=%v error=%v", img, err)
			}
		})
	}
	service.mu.Lock()
	after := len(service.observed)
	service.mu.Unlock()
	if after != previous {
		t.Fatalf("inaccessible TMPDIR dispatched %d screenshot requests", after-previous)
	}
	if _, err := os.Stat(invalid); !os.IsNotExist(err) {
		t.Fatalf("capture created unexpected path or returned wrong stat error: %v", err)
	}
}

func TestGnomeScreenshotDecodeRejectsMissingAndInvalidPNG(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "absent.png")
	if img, err := decodeScreenshot(missing); img != nil || err == nil || !strings.Contains(err.Error(), "open") {
		t.Fatalf("missing screenshot image=%v err=%v", img, err)
	}
	malformed := filepath.Join(dir, "malformed.png")
	if err := os.WriteFile(malformed, []byte("not a PNG"), 0600); err != nil {
		t.Fatal(err)
	}
	if img, err := decodeScreenshot(malformed); img != nil || err == nil || !strings.Contains(err.Error(), "decode PNG") {
		t.Fatalf("corrupt screenshot image=%v err=%v", img, err)
	}
}
