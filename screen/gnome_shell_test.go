package screen

import (
	"context"
	"errors"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/env"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestGnomeShellCaptureRejectsUnrepresentableRegionsBeforeDBus(t *testing.T) {
	backend, provider := newSyntheticGnomeScreenshotBackend(t)
	tests := []struct {
		name string
		rect image.Rectangle
		want string
	}{
		{"negative x origin overflow", image.Rect(-1<<40, 0, (-1<<40)+1, 1), "origin"},
		{"positive y origin overflow", image.Rect(0, 1<<40, 1, (1<<40)+1), "origin"},
		{"x span overflow", image.Rect(-100, 0, 1<<32, 1), "size"},
		{"y span overflow", image.Rect(0, -100, 1, 1<<32), "size"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider.mu.Lock()
			before := len(provider.observed)
			provider.mu.Unlock()
			got, err := backend.Grab(context.Background(), tt.rect)
			if got != nil || err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("invalid GNOME capture returned image=%v err=%v, want %q", got, err, tt.want)
			}
			provider.mu.Lock()
			after := len(provider.observed)
			provider.mu.Unlock()
			if after != before {
				t.Fatalf("unrepresentable rectangle reached compositor: calls %d -> %d", before, after)
			}
		})
	}
}

func TestGnomeShellCanceledCapturePreservesErrorAndAvoidsDBus(t *testing.T) {
	backend, provider := newSyntheticGnomeScreenshotBackend(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	provider.mu.Lock()
	before := len(provider.observed)
	provider.mu.Unlock()
	for _, rect := range []image.Rectangle{{}, image.Rect(5, 6, 7, 8)} {
		img, err := backend.Grab(ctx, rect)
		if img != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled capture = %v %v", img, err)
		}
		hash, err := backend.GrabRegionHash(ctx, rect)
		if hash != 0 || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled hash = %d %v", hash, err)
		}
	}
	provider.mu.Lock()
	after := len(provider.observed)
	provider.mu.Unlock()
	if after != before {
		t.Fatalf("canceled capture contacted GNOME service: %d -> %d", before, after)
	}
}

func TestGnomeShellCaptureMethodNamesAreBounded(t *testing.T) {
	for _, tt := range []struct{ input, want string }{
		{"org.gnome.Shell.Screenshot.Screenshot", "Screenshot"},
		{"org.gnome.Shell.Screenshot.ScreenshotArea", "ScreenshotArea"},
		{"Screenshot", "Screenshot"},
		{"", ""},
		{"suffix.", ""},
	} {
		if got := methodName(tt.input); got != tt.want {
			t.Errorf("methodName(%q)=%q want %q", tt.input, got, tt.want)
		}
	}
}

// Probe must distinguish an absent screenshot service from a working, authorized
// GNOME screenshot service on a real isolated session bus.
func TestGnomeShellScreenshotProbePrivateBusAvailability(t *testing.T) {
	_, portal := newSyntheticScreenshotPortal(t)
	rt := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + portal.busAddress})
	missing := checkGnomeShellScreenshot(rt, compositor.GNOME)
	if missing.Available || !strings.Contains(missing.Reason, "not on session bus") {
		t.Fatalf("missing GNOME service advertised: %+v", missing)
	}
	fake := &syntheticGnomeScreenshot{success: true}
	reply, err := portal.conn.RequestName(gnomeShellShotDest, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("register screenshot provider: %v, %v", reply, err)
	}
	if err := portal.conn.Export(fake, gnomeShellShotPath, gnomeShellShotIface); err != nil {
		t.Fatal(err)
	}
	available := checkGnomeShellScreenshot(rt, compositor.GNOME)
	if !available.Available || !strings.Contains(available.Reason, "unsafe mode") {
		t.Fatalf("authorized GNOME service not advertised: %+v", available)
	}
	fake.mu.Lock()
	observed := len(fake.observed)
	fake.deny = true
	fake.mu.Unlock()
	if observed == 0 {
		t.Fatal("probe did not perform an authorization capture")
	}
	denied := checkGnomeShellScreenshot(rt, compositor.GNOME)
	if denied.Available || !strings.Contains(denied.Reason, "permission denied") {
		t.Fatalf("denied GNOME service advertised: %+v", denied)
	}
}

type syntheticGnomeScreenshot struct {
	mu                     sync.Mutex
	success                bool
	alternate              string
	deny                   bool
	brokenPNG              bool
	returnEmptyPath        bool
	replaceWithNonemptyDir bool
	transportPath          string
	observed               []string
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
	if s.replaceWithNonemptyDir {
		if err := os.Remove(path); err != nil {
			return false, "", dbus.MakeFailedError(err)
		}
		if err := os.Mkdir(path, 0700); err != nil {
			return false, "", dbus.MakeFailedError(err)
		}
		if err := os.WriteFile(filepath.Join(path, "preserve"), []byte("untrusted replacement"), 0600); err != nil {
			return false, "", dbus.MakeFailedError(err)
		}
		s.transportPath = path
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
	if s.returnEmptyPath {
		return true, "", nil
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

type malformedGnomeScreenshotReply struct{}

func (malformedGnomeScreenshotReply) Screenshot(bool, bool, string) (string, *dbus.Error) {
	return "unexpected reply shape", nil
}

func (malformedGnomeScreenshotReply) ScreenshotArea(int32, int32, int32, int32, bool, string) (string, *dbus.Error) {
	return "unexpected reply shape", nil
}

func TestGnomeShellRejectsMissingBusAndMalformedReply(t *testing.T) {
	if backend, err := NewGnomeShellScreenshotBackendForBus(""); backend != nil || err == nil || !strings.Contains(err.Error(), "D-Bus session unset") {
		t.Fatalf("empty session bus address returned backend=%v err=%v", backend, err)
	}

	_, portal := newSyntheticScreenshotPortal(t)
	reply, err := portal.conn.RequestName(gnomeShellShotDest, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim GNOME screenshot service: reply=%v err=%v", reply, err)
	}
	if err := portal.conn.Export(malformedGnomeScreenshotReply{}, gnomeShellShotPath, gnomeShellShotIface); err != nil {
		t.Fatal(err)
	}
	backend, err := NewGnomeShellScreenshotBackendForBusContext(context.Background(), portal.busAddress)
	if backend != nil || err == nil || !strings.Contains(err.Error(), "reply") {
		t.Fatalf("malformed screenshot reply returned backend=%v err=%v", backend, err)
	}
}

func TestGnomeShellUsesOwnedPathWhenServiceReturnsNoPath(t *testing.T) {
	backend, provider := newSyntheticGnomeScreenshotBackend(t)
	provider.mu.Lock()
	provider.returnEmptyPath = true
	provider.mu.Unlock()

	img, err := backend.Grab(context.Background(), image.Rectangle{})
	if err != nil || img == nil || img.Bounds() != image.Rect(0, 0, 4, 4) {
		t.Fatalf("empty service path did not use the owned screenshot transport: image=%v err=%v", img, err)
	}
}

func TestGnomeShellReportsCleanupFailureWithoutDeletingReplacementContents(t *testing.T) {
	backend, provider := newSyntheticGnomeScreenshotBackend(t)
	t.Cleanup(func() {
		provider.mu.Lock()
		path := provider.transportPath
		provider.mu.Unlock()
		if path != "" {
			_ = os.RemoveAll(path)
		}
	})
	provider.mu.Lock()
	provider.replaceWithNonemptyDir = true
	provider.mu.Unlock()

	img, err := backend.Grab(context.Background(), image.Rectangle{})
	if img != nil || err == nil || !strings.Contains(err.Error(), "remove transport") {
		t.Fatalf("replacement transport returned image=%v err=%v; want reported cleanup failure", img, err)
	}

	provider.mu.Lock()
	path := provider.transportPath
	provider.mu.Unlock()
	data, readErr := os.ReadFile(filepath.Join(path, "preserve"))
	if readErr != nil || string(data) != "untrusted replacement" {
		t.Fatalf("cleanup removed or altered replacement contents: %q %v", data, readErr)
	}
}

func TestGnomeScreenshotDecodeRejectsMissingParent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-parent", "capture.png")
	if img, err := decodeScreenshot(path); img != nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("decode with missing parent returned image=%v err=%v", img, err)
	}
}
