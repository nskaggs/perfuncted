//go:build linux

package screen

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"github.com/nskaggs/perfuncted/internal/gnomebridge"
)

type privateGnomeCore struct{}

func (privateGnomeCore) GetProtocolVersion() (uint32, *dbus.Error) {
	return gnomebridge.ProtocolVersion, nil
}
func (privateGnomeCore) GetExtensionVersion() (string, *dbus.Error) {
	return gnomebridge.ExtensionVersion, nil
}
func (privateGnomeCore) GetShellVersion() (string, *dbus.Error) { return "51.0", nil }
func (privateGnomeCore) GetCapabilities() ([]string, *dbus.Error) {
	return []string{gnomebridge.CapabilityScreen}, nil
}

type privateGnomeCapture struct {
	mu                 sync.Mutex
	captures           []string
	reject             bool
	invalidPNG         bool
	mismatchedGeometry bool
}

func (p *privateGnomeCapture) capture(fd dbus.UnixFD, kind string) (int32, int32, int32, int32, int32, int32, float64, *dbus.Error) {
	p.mu.Lock()
	p.captures = append(p.captures, kind)
	reject, invalid, mismatch := p.reject, p.invalidPNG, p.mismatchedGeometry
	p.mu.Unlock()
	if reject {
		return 0, 0, 0, 0, 0, 0, 0, dbus.MakeFailedError(errors.New("capture rejected"))
	}
	f := os.NewFile(uintptr(fd), "private-gnome-image")
	defer f.Close()
	if invalid {
		if _, err := f.Write([]byte("bad image")); err != nil {
			return 0, 0, 0, 0, 0, 0, 0, dbus.MakeFailedError(err)
		}
	} else {
		img := image.NewRGBA(image.Rect(0, 0, 4, 4))
		img.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
		img.SetRGBA(2, 2, color.RGBA{G: 255, A: 255})
		if err := png.Encode(f, img); err != nil {
			return 0, 0, 0, 0, 0, 0, 0, dbus.MakeFailedError(err)
		}
	}
	if mismatch {
		return 0, 0, 2, 2, 1, 1, 2, nil
	}
	return 0, 0, 2, 2, 4, 4, 2, nil
}
func (p *privateGnomeCapture) CaptureFull(fd dbus.UnixFD) (int32, int32, int32, int32, int32, int32, float64, *dbus.Error) {
	return p.capture(fd, "full")
}
func (p *privateGnomeCapture) CaptureRegion(fd dbus.UnixFD, x, y, w, h int32) (int32, int32, int32, int32, int32, int32, float64, *dbus.Error) {
	if w <= 0 || h <= 0 {
		return 0, 0, 0, 0, 0, 0, 0, dbus.MakeFailedError(errors.New("invalid requested region"))
	}
	return p.capture(fd, "region")
}
func newPrivateGnomeNativeCapture(t *testing.T) (*GnomeNativeScreenBackend, *privateGnomeCapture) {
	t.Helper()
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("private D-Bus server unavailable")
	}
	address := "unix:path=" + filepath.Join(t.TempDir(), "private-bus")
	proc := exec.Command("dbus-daemon", "--session", "--nofork", "--address="+address)
	if err := proc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proc.Process.Kill(); _ = proc.Wait() })
	var service *dbus.Conn
	var err error
	for i := 0; i < 100; i++ {
		service, err = dbusutil.SessionBusAddressContext(context.Background(), address)
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	reply, err := service.RequestName(gnomebridge.BusName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("register service: reply=%v err=%v", reply, err)
	}
	if lintShadow109 := service.Export(privateGnomeCore{}, dbus.ObjectPath(gnomebridge.ObjectPath), gnomebridge.CoreInterface); lintShadow109 != nil {
		t.Fatal(lintShadow109)
	}
	capture := &privateGnomeCapture{}
	if lintShadow113 := service.Export(capture, dbus.ObjectPath(gnomebridge.ObjectPath), gnomebridge.ScreenInterface); lintShadow113 != nil {
		t.Fatal(lintShadow113)
	}
	client, err := gnomebridge.NewClientForBus(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return &GnomeNativeScreenBackend{bridge: client}, capture
}

func TestGnomeNativePrivateBusCaptureIsPixelCorrectAndReleasesOwnedFiles(t *testing.T) {
	b, provider := newPrivateGnomeNativeCapture(t)
	ctx := context.Background()
	full, err := b.Grab(ctx, image.Rectangle{})
	if err != nil {
		t.Fatal(err)
	}
	if full.Bounds() != image.Rect(0, 0, 2, 2) || !isSyntheticPixelColor(full.At(0, 0), 255, 0) || !isSyntheticPixelColor(full.At(1, 1), 0, 255) {
		t.Fatalf("wrong GNOME logical full image: bounds=%v", full.Bounds())
	}
	region, err := b.Grab(ctx, image.Rect(5, 7, 7, 9))
	if err != nil {
		t.Fatal(err)
	}
	if region.Bounds() != image.Rect(0, 0, 2, 2) {
		t.Fatalf("GNOME region geometry changed: %v", region.Bounds())
	}
	fullHash, err := b.GrabFullHash(ctx)
	if err != nil || fullHash != find.PixelHash(full, nil) {
		t.Fatalf("full hash mismatch: %08x %v", fullHash, err)
	}
	regionHash, err := b.GrabRegionHash(ctx, image.Rect(5, 7, 7, 9))
	if err != nil || regionHash != find.PixelHash(region, nil) {
		t.Fatalf("region hash mismatch: %08x %v", regionHash, err)
	}
	w, h, err := b.Resolution()
	if err != nil || w != 2 || h != 2 {
		t.Fatalf("resolution %dx%d %v", w, h, err)
	}
	if !b.CanonicalHashing() {
		t.Fatal("native capture did not advertise canonical pixel hashing")
	}
	provider.mu.Lock()
	counts := len(provider.captures)
	provider.mu.Unlock()
	if counts != 5 {
		t.Fatalf("native bridge capture count = %d, want 5", counts)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if img, err := b.Grab(context.Background(), image.Rectangle{}); err == nil || img != nil {
		t.Fatalf("closed GNOME bridge returned image %v %v", img, err)
	}
}

func TestGnomeNativePrivateBusDenyAndCorruptCaptureFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name          string
		deny, corrupt bool
	}{
		{"permission denial", true, false}, {"malformed PNG", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, provider := newPrivateGnomeNativeCapture(t)
			provider.mu.Lock()
			provider.reject = tc.deny
			provider.invalidPNG = tc.corrupt
			provider.mu.Unlock()
			img, err := b.Grab(context.Background(), image.Rectangle{})
			if err == nil || img != nil {
				t.Fatalf("untrusted capture accepted: %v %v", img, err)
			}
		})
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	b, provider := newPrivateGnomeNativeCapture(t)
	if _, err := b.Grab(canceled, image.Rectangle{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled capture accepted: %v", err)
	}
	provider.mu.Lock()
	attempted := len(provider.captures)
	provider.mu.Unlock()
	if attempted != 0 {
		t.Fatal("canceled capture reached GNOME service")
	}
}

func TestGnomeNativeCaptureTemporaryFileRemovedOnSuccessAndFailedDecode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	b, provider := newPrivateGnomeNativeCapture(t)
	if _, err := b.Grab(context.Background(), image.Rectangle{}); err != nil {
		t.Fatal(err)
	}
	if files, err := filepath.Glob(filepath.Join(dir, "perfuncted-gnome-capture-*")); err != nil || len(files) > 0 {
		t.Fatalf("successful GNOME capture left transport files: %v %v", files, err)
	}
	provider.mu.Lock()
	provider.mismatchedGeometry = true
	provider.mu.Unlock()
	if img, err := b.Grab(context.Background(), image.Rectangle{}); err == nil || img != nil {
		t.Fatalf("mismatched provider dimensions accepted: image=%v err=%v", img, err)
	}
	if files, err := filepath.Glob(filepath.Join(dir, "perfuncted-gnome-capture-*")); err != nil || len(files) > 0 {
		t.Fatalf("failed GNOME capture left transport files: %v %v", files, err)
	}
}
func TestGnomeNativeUninitializedBackendAndCanceledHashOperationsFailClosed(t *testing.T) {
	for _, backend := range []*GnomeNativeScreenBackend{nil, {}} {
		if img, err := backend.Grab(context.Background(), image.Rectangle{}); err == nil || img != nil {
			t.Fatalf("missing bridge accepted capture: %v %v", img, err)
		}
		if _, err := backend.GrabFullHash(context.Background()); err == nil {
			t.Fatal("missing bridge accepted full hash")
		}
		if _, err := backend.GrabRegionHash(context.Background(), image.Rect(0, 0, 2, 2)); err == nil {
			t.Fatal("missing bridge accepted region hash")
		}
		if w, h, err := backend.ResolutionWithContext(context.Background()); err == nil || w != 0 || h != 0 {
			t.Fatalf("missing bridge resolution=%dx%d %v", w, h, err)
		}
		if err := backend.Close(); err != nil {
			t.Fatalf("nil bridge close: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend, provider := newPrivateGnomeNativeCapture(t)
	if _, err := backend.GrabFullHash(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled hash: %v", err)
	}
	if _, err := backend.GrabRegionHash(ctx, image.Rect(0, 0, 2, 2)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled region hash: %v", err)
	}
	if _, _, err := backend.ResolutionWithContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled resolution: %v", err)
	}
	provider.mu.Lock()
	invocations := len(provider.captures)
	provider.mu.Unlock()
	if invocations != 0 {
		t.Fatal("canceled operations reached screenshot service")
	}
}

func isSyntheticPixelColor(pixel color.Color, red, green uint8) bool {
	rgba, ok := pixel.(color.RGBA)
	return ok && rgba.R == red && rgba.G == green
}
