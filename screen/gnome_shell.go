//go:build linux
// +build linux

package screen

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"os"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

// GrabFullHash returns a fast pixel hash of the entire screen.
func (b *GnomeShellScreenshotBackend) GrabFullHash(ctx context.Context) (uint32, error) {
	img, err := b.Grab(ctx, image.Rectangle{})
	if err != nil {
		return 0, err
	}
	return find.PixelHash(img, nil), nil
}

func (b *GnomeShellScreenshotBackend) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	img, err := b.Grab(ctx, rect)
	if err != nil {
		return 0, err
	}
	return find.PixelHash(img, nil), nil
}

const (
	gnomeShellShotDest  = "org.gnome.Shell.Screenshot"
	gnomeShellShotPath  = "/org/gnome/Shell/Screenshot"
	gnomeShellShotIface = "org.gnome.Shell.Screenshot"
)

// GnomeShellScreenshotBackend captures the screen through GNOME Shell's native
// org.gnome.Shell.Screenshot D-Bus service. GNOME Shell restricts this service
// to trusted callers unless the shell is running in unsafe mode.
type GnomeShellScreenshotBackend struct {
	conn *dbus.Conn
	obj  dbus.BusObject
}

// NewGnomeShellScreenshotBackend returns a backend when GNOME Shell's screenshot
// service is reachable and the current process is allowed to call it. A 1x1
// probe screenshot is performed at construction time so callers can fall back to
// the portal when unsafe mode is disabled.
func NewGnomeShellScreenshotBackend() (*GnomeShellScreenshotBackend, error) {
	return NewGnomeShellScreenshotBackendForBus("")
}

// NewGnomeShellScreenshotBackendForBus returns a backend for the session bus at
// addr when GNOME Shell's screenshot service is reachable and authorized.
func NewGnomeShellScreenshotBackendForBus(addr string) (*GnomeShellScreenshotBackend, error) {
	conn, err := dbusutil.SessionBusAddress(addr)
	if err != nil {
		return nil, fmt.Errorf("screen/gnome-shell: D-Bus session: %w", err)
	}
	if !dbusutil.HasService(conn, gnomeShellShotDest) {
		conn.Close()
		return nil, fmt.Errorf("screen/gnome-shell: %s not on session bus", gnomeShellShotDest)
	}
	b := &GnomeShellScreenshotBackend{
		conn: conn,
		obj:  conn.Object(gnomeShellShotDest, gnomeShellShotPath),
	}
	if _, err := b.Grab(context.Background(), image.Rect(0, 0, 1, 1)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("screen/gnome-shell: authorization check failed: %w", err)
	}
	return b, nil
}

func newTempScreenshotFile(prefix string) (*os.File, error) {
	f, err := os.CreateTemp("", prefix)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// Grab captures rect using GNOME Shell's native screenshot service. A zero rect
// requests a full-screen capture; a non-empty rect uses ScreenshotArea.
func (b *GnomeShellScreenshotBackend) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	tmp, err := newTempScreenshotFile("perfuncted-gnome-*.png")
	if err != nil {
		return nil, fmt.Errorf("screen/gnome-shell: temp file: %w", err)
	}
	path := tmp.Name()
	tmp.Close()
	defer os.Remove(path) //nolint:errcheck

	var success bool
	var used string

	if rect.Empty() {
		call := b.obj.Call(gnomeShellShotIface+".Screenshot", 0, false, false, path)
		if call.Err != nil {
			return nil, fmt.Errorf("screen/gnome-shell: Screenshot: %w", call.Err)
		}
		err = call.Store(&success, &used)
		if err != nil {
			return nil, fmt.Errorf("screen/gnome-shell: Screenshot reply: %w", err)
		}
	} else {
		call := b.obj.Call(gnomeShellShotIface+".ScreenshotArea", 0,
			int32(rect.Min.X), int32(rect.Min.Y), int32(rect.Dx()), int32(rect.Dy()),
			false, path,
		)
		if call.Err != nil {
			return nil, fmt.Errorf("screen/gnome-shell: ScreenshotArea: %w", call.Err)
		}
		err = call.Store(&success, &used)
		if err != nil {
			return nil, fmt.Errorf("screen/gnome-shell: ScreenshotArea reply: %w", err)
		}
	}

	if !success {
		return nil, fmt.Errorf("screen/gnome-shell: capture failed")
	}
	if used == "" {
		used = path
	}
	if used != path {
		defer os.Remove(used) //nolint:errcheck
	}

	f, err := os.Open(used)
	if err != nil {
		return nil, fmt.Errorf("screen/gnome-shell: open %s: %w", used, err)
	}
	defer f.Close()

	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("screen/gnome-shell: decode PNG: %w", err)
	}
	return img, nil
}

func (b *GnomeShellScreenshotBackend) Resolution() (int, int, error) {
	img, err := b.Grab(context.Background(), image.Rect(0, 0, 0, 0))
	if err != nil {
		return 0, 0, err
	}
	bounds := img.Bounds()
	return bounds.Dx(), bounds.Dy(), nil
}

func (b *GnomeShellScreenshotBackend) Close() error { return b.conn.Close() }
