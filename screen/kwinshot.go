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
	"github.com/nskaggs/perfuncted/ctxutil"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

var _ Screenshotter = (*KWinShotBackend)(nil)

const (
	kwinShotDest  = "org.kde.KWin"
	kwinShotPath  = "/org/kde/KWin/ScreenShot2"
	kwinShotIface = "org.kde.KWin.ScreenShot2"
)

type kwinShotTransport interface {
	CaptureArea(context.Context, image.Rectangle) (image.Image, error)
	CaptureActiveScreen(context.Context) (image.Image, error)
}

// KWinShotBackend captures the screen via org.kde.KWin.ScreenShot2.
type KWinShotBackend struct {
	transport kwinShotTransport
}

type kwinDBusTransport struct {
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
	if addr == "" {
		return nil, fmt.Errorf("screen/kwin: D-Bus session unset")
	}
	conn, err := dbusutil.SessionBusAddress(addr)
	if err != nil {
		return nil, fmt.Errorf("screen/kwin: D-Bus session: %w", err)
	}
	if !dbusutil.HasService(conn, kwinShotDest) {
		return nil, fmt.Errorf("screen/kwin: %s not on session bus", kwinShotDest)
	}
	obj := conn.Object(kwinShotDest, kwinShotPath)
	b := &KWinShotBackend{transport: &kwinDBusTransport{conn: conn, kwin: obj}}
	// Probe grab: verify the process has screenshot authorization.
	// KDE Plasma 6 requires explicit per-process permission via the xdg
	// permission store; this check allows Open() to fall back to the portal.
	if _, err := b.Grab(context.Background(), image.Rect(0, 0, 1, 1)); err != nil {
		return nil, fmt.Errorf("screen/kwin: authorization check failed: %w", err)
	}
	return b, nil
}

// Grab captures rect using CaptureArea. An empty rect requests the active
// screen capture path. KWin writes pixel data to a Unix pipe; the D-Bus reply
// carries format metadata in an a{sv}. We close our write-end copy after the
// synchronous call returns (KWin has already written + closed its dup'd copy
// by then), then drain the read end.
func (b *KWinShotBackend) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	if b == nil || b.transport == nil {
		return nil, fmt.Errorf("screen/kwin: backend not initialised")
	}
	if rect.Empty() {
		return b.transport.CaptureActiveScreen(ctx)
	}
	return b.transport.CaptureArea(ctx, rect)
}

// GrabFullHash returns a fast pixel hash of the active screen.
func (b *KWinShotBackend) GrabFullHash(ctx context.Context) (uint32, error) {
	img, err := b.Grab(ctx, image.Rectangle{})
	if err != nil {
		return 0, err
	}
	return find.PixelHash(img, nil), nil
}

// GrabRegionHash computes a CRC32 pixel fingerprint for rect. For KWin this
// falls back to Grab + PixelHash so the hash stays consistent with GrabFullHash.
func (b *KWinShotBackend) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	if rect.Empty() {
		return b.GrabFullHash(ctx)
	}
	img, err := b.Grab(ctx, rect)
	if err != nil {
		return 0, err
	}
	return find.PixelHash(img, nil), nil
}

func (t *kwinDBusTransport) CaptureArea(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	if rect.Empty() {
		return nil, fmt.Errorf("screen/kwin: empty rectangle")
	}
	return t.capture(ctx, kwinShotIface+".CaptureArea", rect,
		int32(rect.Min.X), int32(rect.Min.Y),
		uint32(rect.Dx()), uint32(rect.Dy()),
	)
}

func (t *kwinDBusTransport) CaptureActiveScreen(ctx context.Context) (image.Image, error) {
	return t.capture(ctx, kwinShotIface+".CaptureActiveScreen", image.Rectangle{})
}

func (t *kwinDBusTransport) capture(ctx context.Context, method string, rect image.Rectangle, args ...interface{}) (image.Image, error) {
	ctx = ctxutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("screen/kwin: capture canceled: %w", err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("screen/kwin: pipe: %w", err)
	}
	defer r.Close()

	callArgs := append(args, map[string]dbus.Variant{}, dbus.UnixFD(w.Fd()))
	var results map[string]dbus.Variant
	call := t.kwin.Call(method, 0, callArgs...)
	// Close our copy now; KWin received its own via SCM_RIGHTS and has already
	// written + closed its copy by the time Call() returns (it's synchronous).
	w.Close()

	if call.Err != nil {
		return nil, fmt.Errorf("screen/kwin: %s: %w", method, call.Err)
	}
	if err := call.Store(&results); err != nil {
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

	w, h := kwinImageSize(rect, results)
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

func kwinImageSize(rect image.Rectangle, results map[string]dbus.Variant) (w, h int) {
	w, h = rect.Dx(), rect.Dy()
	if results == nil {
		return w, h
	}
	if sv, ok := results["width"]; ok {
		if s, ok := sv.Value().(uint32); ok && s > 0 {
			w = int(s)
		}
	}
	if sv, ok := results["height"]; ok {
		if s, ok := sv.Value().(uint32); ok && s > 0 {
			h = int(s)
		}
	}
	return w, h
}

// Resolution returns the active screen dimensions.
func (b *KWinShotBackend) Resolution() (int, int, error) {
	img, err := b.Grab(context.Background(), image.Rectangle{})
	if err != nil {
		return 0, 0, err
	}
	bounds := img.Bounds()
	return bounds.Dx(), bounds.Dy(), nil
}

// Close closes the D-Bus connection held by the backend.
func (b *KWinShotBackend) Close() error {
	if b == nil || b.transport == nil {
		return nil
	}
	if t, ok := b.transport.(*kwinDBusTransport); ok && t.conn != nil {
		return t.conn.Close()
	}
	return nil
}
