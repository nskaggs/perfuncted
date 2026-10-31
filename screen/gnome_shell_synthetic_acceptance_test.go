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
	"sync"
	"testing"

	"github.com/godbus/dbus/v5"
)

type syntheticGnomeScreenshot struct {
	mu        sync.Mutex
	success   bool
	alternate string
	deny      bool
	brokenPNG bool
	observed  []string
}

func (s *syntheticGnomeScreenshot) Screenshot(_ bool, _ bool, path string) (bool, string, *dbus.Error) {
	return s.capture(path, "full")
}
func (s *syntheticGnomeScreenshot) ScreenshotArea(x, y, w, h int32, _ bool, path string) (bool, string, *dbus.Error) {
	return s.capture(path, "area")
}
func (s *syntheticGnomeScreenshot) capture(path, kind string) (bool, string, *dbus.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observed = append(s.observed, kind)
	if s.deny {
		return false, "", dbus.MakeFailedError(errors.New("permission denied"))
	}
	if !s.success {
		return false, "", nil
	}
	if s.brokenPNG {
		if err := os.WriteFile(path, []byte("malformed"), 0600); err != nil {
			return false, "", dbus.MakeFailedError(err)
		}
		return true, path, nil
	}
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	file, err := os.Create(path)
	if err != nil {
		return false, "", dbus.MakeFailedError(err)
	}
	if err := png.Encode(file, img); err != nil {
		_ = file.Close()
		return false, "", dbus.MakeFailedError(err)
	}
	if err := file.Close(); err != nil {
		return false, "", dbus.MakeFailedError(err)
	}
	if s.alternate != "" {
		return true, s.alternate, nil
	}
	return true, path, nil
}
func newSyntheticGnomeScreenshotBackend(t *testing.T) (*GnomeShellScreenshotBackend, *syntheticGnomeScreenshot) {
	t.Helper()
	portalBackend, portal := newSyntheticScreenshotPortal(t)
	fake := &syntheticGnomeScreenshot{success: true}
	reply, err := portal.conn.RequestName(gnomeShellShotDest, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("register GNOME screenshot service: %v, %v", reply, err)
	}
	if lintShadow75 := portal.conn.Export(fake, gnomeShellShotPath, gnomeShellShotIface); lintShadow75 != nil {
		t.Fatal(lintShadow75)
	}
	backend, err := NewGnomeShellScreenshotBackendForBusContext(context.Background(), portal.busAddress)
	if err != nil {
		t.Fatalf("constructor rejected authorized GNOME Screenshot service: %v", err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	_ = portalBackend
	return backend, fake
}

func TestSyntheticGnomeScreenshotCapturesHashesAndCleansOwnedFiles(t *testing.T) {
	backend, provider := newSyntheticGnomeScreenshotBackend(t)
	ctx := context.Background()
	img, err := backend.Grab(ctx, image.Rect(0, 0, 2, 2))
	if err != nil || img.Bounds() != image.Rect(0, 0, 4, 4) {
		t.Fatalf("GNOME area capture bounds %v err %v", img.Bounds(), err)
	}
	h, err := backend.GrabFullHash(ctx)
	if err != nil || h == 0 {
		t.Fatalf("full hash %d, %v", h, err)
	}
	h, err = backend.GrabRegionHash(ctx, image.Rect(0, 0, 2, 2))
	if err != nil || h == 0 {
		t.Fatalf("region hash %d, %v", h, err)
	}
	width, height, err := backend.Resolution()
	if err != nil || width != 4 || height != 4 {
		t.Fatalf("resolution %d,%d %v", width, height, err)
	}
	provider.mu.Lock()
	got := strings.Join(provider.observed, ",")
	provider.mu.Unlock()
	if !strings.Contains(got, "full") || !strings.Contains(got, "area") {
		t.Fatalf("missing screenshot interfaces: %v", provider.observed)
	}
}

func TestSyntheticGnomeScreenshotFailsClosedOnDeniedAndMalformedTransport(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*syntheticGnomeScreenshot)
		want      string
	}{
		{"denied", func(p *syntheticGnomeScreenshot) { p.deny = true }, "permission denied"},
		{"explicit failure", func(p *syntheticGnomeScreenshot) { p.success = false }, "capture failed"},
		{"invalid PNG", func(p *syntheticGnomeScreenshot) { p.brokenPNG = true }, "decode PNG"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, provider := newSyntheticGnomeScreenshotBackend(t)
			provider.mu.Lock()
			tc.configure(provider)
			provider.mu.Unlock()
			img, err := backend.Grab(context.Background(), image.Rectangle{})
			if err == nil || img != nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("failed GNOME capture returned img=%v err=%v want %q", img, err, tc.want)
			}
		})
	}
}

func TestSyntheticGnomeScreenshotCannotDeleteUnrequestedPaths(t *testing.T) {
	backend, provider := newSyntheticGnomeScreenshotBackend(t)
	victim := filepath.Join(t.TempDir(), "unrelated.png")
	file, err := os.Create(victim)
	if err != nil {
		t.Fatal(err)
	}
	if lintShadow144 := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 3, 3))); lintShadow144 != nil {
		t.Fatal(lintShadow144)
	}
	if lintShadow147 := file.Close(); lintShadow147 != nil {
		t.Fatal(lintShadow147)
	}
	provider.mu.Lock()
	provider.alternate = victim
	provider.mu.Unlock()
	_, err = backend.Grab(context.Background(), image.Rectangle{})
	if err == nil {
		t.Fatal("accepted unrequested screenshot path")
	}
	if _, statErr := os.Stat(victim); statErr != nil {
		t.Fatalf("unrequested file was deleted or modified: %v", statErr)
	}
}
