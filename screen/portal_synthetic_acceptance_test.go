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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

type syntheticScreenshotPortal struct {
	conn          *dbus.Conn
	busAddress    string
	mu            sync.Mutex
	code          uint32
	malformedCode any
	uri           string
	skip          bool
}

func (p *syntheticScreenshotPortal) Screenshot(_ string, opts map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	token, _ := opts["handle_token"].Value().(string)
	path := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/fixture/" + token)
	p.mu.Lock()
	code, uri, skip := any(p.code), p.uri, p.skip
	if p.malformedCode != nil {
		code = p.malformedCode
	}
	p.mu.Unlock()
	if !skip {
		go func() {
			time.Sleep(10 * time.Millisecond)
			results := map[string]dbus.Variant{"uri": dbus.MakeVariant(uri)}
			_ = p.conn.Emit(path, portalReqIf+".Response", code, results)
		}()
	}
	return path, nil
}
func newSyntheticScreenshotPortal(t *testing.T) (*PortalDBusBackend, *syntheticScreenshotPortal) {
	t.Helper()
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon unavailable")
	}
	dir, err := os.MkdirTemp("", "pf-screen-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "bus")
	addr := "unix:path=" + socket
	proc := exec.Command("dbus-daemon", "--session", "--nofork", "--address="+addr)
	if startErr := proc.Start(); startErr != nil {
		t.Fatal(startErr)
	}
	t.Cleanup(func() { _ = proc.Process.Kill(); _ = proc.Wait() })
	var service *dbus.Conn
	for i := 0; i < 100; i++ {
		service, err = dbusutil.SessionBusAddressContext(context.Background(), addr)
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("connect private bus: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	reply, err := service.RequestName(portalDest, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("register screenshot service: %v, %v", reply, err)
	}
	portal := &syntheticScreenshotPortal{conn: service, busAddress: addr}
	if exportErr := service.Export(portal, portalPath, portalSsIf); exportErr != nil {
		t.Fatal(exportErr)
	}
	backend, err := NewPortalDBusBackendForBusContext(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	return backend, portal
}

func TestSyntheticScreenshotPortalCaptureCropAndHash(t *testing.T) {
	backend, portal := newSyntheticScreenshotPortal(t)
	path := filepath.Join(t.TempDir(), "capture space.png")
	imageData := image.NewRGBA(image.Rect(0, 0, 4, 4))
	imageData.Set(1, 1, color.RGBA{R: 255, A: 255})
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if encodeErr := png.Encode(file, imageData); encodeErr != nil {
		t.Fatal(encodeErr)
	}
	if closeErr := file.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	portal.mu.Lock()
	portal.uri = "file://" + strings.ReplaceAll(path, " ", "%20")
	portal.mu.Unlock()
	img, err := backend.Grab(context.Background(), image.Rect(1, 1, 3, 3))
	if err != nil {
		t.Fatalf("authorized screenshot: %v", err)
	}
	if img.Bounds().Dx() != 2 || img.Bounds().Dy() != 2 {
		t.Fatalf("cropped screenshot bounds %v", img.Bounds())
	}
	if got := color.RGBAModel.Convert(img.At(img.Bounds().Min.X, img.Bounds().Min.Y)).(color.RGBA); got.R != 255 { //nolint:errcheck // RGBAModel.Convert guarantees color.RGBA
		t.Fatalf("unexpected crop pixel %v", got)
	}
	if _, err := backend.GrabRegionHash(context.Background(), image.Rect(1, 1, 3, 3)); err != nil {
		t.Fatalf("region hash: %v", err)
	}
	if _, err := backend.GrabFullHash(context.Background()); err != nil {
		t.Fatalf("full hash: %v", err)
	}
}

func TestSyntheticScreenshotPortalFailsClosedOnPermissionAndFileErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		code uint32
		uri  string
		skip bool
		want string
	}{
		{"permission denied", 1, "", false, "denied"},
		{"non-file URI", 0, "https://invalid.example/capture.png", false, "unsupported"},
		{"missing image", 0, "file:///tmp/perfuncted-unavailable-capture.png", false, "open"},
		{"malformed png", 0, "", false, "parse"},
		{"no response", 0, "", true, "timed out"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, portal := newSyntheticScreenshotPortal(t)
			backend.SetCaptureTimeout(70 * time.Millisecond)
			portal.mu.Lock()
			portal.code = tc.code
			portal.uri = tc.uri
			portal.skip = tc.skip
			portal.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err := backend.Grab(ctx, image.Rectangle{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Grab error %v, want %q", err, tc.want)
			}
			if !strings.Contains(strings.Join(backend.Diagnostics(), " "), "last failure") {
				t.Fatal("capture failure not reflected in diagnostics")
			}
		})
	}
}

func TestSyntheticScreenshotPortalCancellationAndSerialGate(t *testing.T) {
	backend, portal := newSyntheticScreenshotPortal(t)
	portal.mu.Lock()
	portal.skip = true
	portal.mu.Unlock()
	backend.SetCaptureTimeout(100 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := backend.Grab(ctx, image.Rectangle{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled screenshot returned %v", err)
	}
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		_, err := backend.Grab(ctx, image.Rectangle{})
		cancel()
		if err == nil {
			t.Fatal("unanswered portal screenshot unexpectedly succeeded")
		}
	}
}
