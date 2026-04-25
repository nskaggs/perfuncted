// KWin.ScreenShot2 backend for KDE Plasma Wayland.
//
// org.kde.KWin.ScreenShot2.CaptureArea writes raw pixel data to a Unix pipe
// passed as an fd argument. No portal, no user-consent dialog — this is a
// trusted compositor API on KDE and the lowest available layer for screen
// capture (below KWin there is only DRM, which KWin owns).
//go:build linux
// +build linux

package screen

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

// GrabFullHash returns a fast pixel hash of the entire screen.
func (b *KWinShotBackend) GrabFullHash(ctx context.Context) (uint32, error) {
	img, err := b.Grab(ctx, image.Rect(0, 0, 0, 0))
	if err != nil {
		// KWin CaptureArea may fail with empty rect, so we need to get resolution first
		w, h, err := ResolutionWithContext(ctx, b)
		if err != nil {
			return 0, err
		}
		img, err = b.Grab(ctx, image.Rect(0, 0, w, h))
		if err != nil {
			return 0, err
		}
	}
	return find.PixelHash(img, nil), nil
}

const (
	kwinShotDest  = "org.kde.KWin"
	kwinShotPath  = "/org/kde/KWin/ScreenShot2"
	kwinShotIface = "org.kde.KWin.ScreenShot2"
)

// KWinShotBackend captures the screen via org.kde.KWin.ScreenShot2.
type KWinShotBackend struct {
	conn *dbus.Conn
	kwin dbus.BusObject
}

// NewKWinShotBackend returns a KWinShotBackend if org.kde.KWin is reachable
// on the session bus and the process is authorized to capture the screen.
// A 1×1 probe grab is performed at construction time to verify authorization;
// if KDE has not granted capture permission, the constructor returns an error
// so the caller can fall back to another backend (e.g. the portal).
func NewKWinShotBackend() (*KWinShotBackend, error) {
	return NewKWinShotBackendForBus("")
}

// NewKWinShotBackendForBus opens a KWin screenshot backend on the session bus
// at addr and verifies that the caller is authorized to capture the screen.
func NewKWinShotBackendForBus(addr string) (*KWinShotBackend, error) {
	conn, err := dbusutil.SessionBusAddress(addr)
	if err != nil {
		return nil, fmt.Errorf("screen/kwin: D-Bus session: %w", err)
	}
	if !dbusutil.HasService(conn, kwinShotDest) {
		return nil, fmt.Errorf("screen/kwin: %s not on session bus", kwinShotDest)
	}
	obj := conn.Object(kwinShotDest, kwinShotPath)
	b := &KWinShotBackend{conn: conn, kwin: obj}
	// Probe grab: verify the process has screenshot authorization.
	// KDE Plasma 6 requires explicit per-process permission via the xdg
	// permission store; this check allows Open() to fall back to the portal.
	if _, err := b.Grab(context.Background(), image.Rect(0, 0, 1, 1)); err != nil {
		return nil, fmt.Errorf("screen/kwin: authorization check failed: %w", err)
	}
	return b, nil
}

// Grab captures rect using CaptureArea. KWin writes pixel data to a Unix pipe;
// the D-Bus reply carries format metadata in an a{sv}. We close our write-end
// copy after the synchronous call returns (KWin has already written + closed
// its dup'd copy by then), then drain the read end.
func (b *KWinShotBackend) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	if rect.Empty() {
		return nil, fmt.Errorf("screen/kwin: empty rectangle")
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("screen/kwin: pipe: %w", err)
	}
	defer r.Close()

	var results map[string]dbus.Variant
	call := b.kwin.Call(kwinShotIface+".CaptureArea", 0,
		int32(rect.Min.X), int32(rect.Min.Y),
		uint32(rect.Dx()), uint32(rect.Dy()),
		map[string]dbus.Variant{}, // options: use defaults
		dbus.UnixFD(w.Fd()),
	)
	// Close our copy now; KWin received its own via SCM_RIGHTS and has already
	// written + closed its copy by the time Call() returns (it's synchronous).
	w.Close()

	if call.Err != nil {
		return nil, fmt.Errorf("screen/kwin: CaptureArea: %w", call.Err)
	}
	err = call.Store(&results)
	if err != nil {
		return nil, fmt.Errorf("screen/kwin: store results: %w", err)
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("screen/kwin: read pipe: %w", err)
	}
	return decodeKWinPixels(data, rect, results)
}

// decodeKWinPixels decodes raw pixel data from the KWin ScreenShot2 pipe.
// It tries PNG first (some KWin builds write a PNG to the pipe rather than
// raw pixels), then falls back to raw BGRA/RGBA decoding.
//
// QImage format constants used for raw path:
//
//	17 = Format_RGBA8888  → bytes: R, G, B, A
//	 4 = Format_ARGB32    → bytes on LE: B, G, R, A  (default GPU framebuffer)
func decodeKWinPixels(data []byte, rect image.Rectangle, results map[string]dbus.Variant) (image.Image, error) {
	if img, err := png.Decode(bytes.NewReader(data)); err == nil {
		return img, nil
	}

	w, h := rect.Dx(), rect.Dy()
	stride := w * 4
	if sv, ok := results["stride"]; ok {
		if s, ok := sv.Value().(uint32); ok && s > 0 {
			stride = int(s)
		}
	}

	// QImage::Format_RGBA8888 = 17 stores R,G,B,A in byte order.
	// All other common formats (Format_ARGB32 = 4) store B,G,R,A on LE.
	isRGBA := false
	if fv, ok := results["format"]; ok {
		if f, ok := fv.Value().(uint32); ok && f == 17 {
			isRGBA = true
		}
	}

	if len(data) < h*stride {
		return nil, fmt.Errorf("screen/kwin: short pixel buffer: got %d bytes, want %d (stride=%d)", len(data), h*stride, stride)
	}

	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			off := y*stride + x*4
			var rv, gv, bv, av byte
			if isRGBA {
				rv, gv, bv, av = data[off], data[off+1], data[off+2], data[off+3]
			} else {
				bv, gv, rv, av = data[off], data[off+1], data[off+2], data[off+3]
			}
			img.SetNRGBA(x, y, color.NRGBA{R: rv, G: gv, B: bv, A: av})
		}
	}
	return img, nil
}

// Close is a no-op; the session bus connection is shared and managed globally.
func (b *KWinShotBackend) Close() error { return nil }
