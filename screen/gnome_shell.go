//go:build linux
// +build linux

package screen

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/internal/contextutil"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

var _ Screenshotter = (*GnomeShellScreenshotBackend)(nil)

// GrabFullHash returns a fast pixel hash of the entire screen.
func (b *GnomeShellScreenshotBackend) GrabFullHash(ctx context.Context) (uint32, error) {
	img, err := b.Grab(ctx, image.Rectangle{})
	if err != nil {
		return 0, err
	}
	return find.PixelHash(img, nil), nil
}

// GrabRegionHash returns a fast pixel hash of rect.
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

// NewGnomeShellScreenshotBackendForBus returns a backend for the session bus at
// addr when GNOME Shell's screenshot service is reachable and authorized.
func NewGnomeShellScreenshotBackendForBus(addr string) (*GnomeShellScreenshotBackend, error) {
	if addr == "" {
		return nil, fmt.Errorf("screen/gnome-shell: D-Bus session unset")
	}
	conn, err := dbusutil.SessionBusAddress(addr)
	if err != nil {
		return nil, fmt.Errorf("screen/gnome-shell: D-Bus session: %w", err)
	}
	if !dbusutil.HasService(conn, gnomeShellShotDest) {
		return nil, errors.Join(
			fmt.Errorf("screen/gnome-shell: %s not on session bus", gnomeShellShotDest),
			conn.Close(),
		)
	}
	b := &GnomeShellScreenshotBackend{
		conn: conn,
		obj:  conn.Object(gnomeShellShotDest, gnomeShellShotPath),
	}
	if _, err := b.Grab(context.Background(), image.Rect(0, 0, 1, 1)); err != nil {
		return nil, errors.Join(
			fmt.Errorf("screen/gnome-shell: authorization check failed: %w", err),
			conn.Close(),
		)
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

func removeScreenshotIfOwned(path, requestedPath string) error {
	if path == requestedPath {
		return os.Remove(path)
	}
	return nil
}

func openScreenshotFile(path string) (*os.File, *os.Root, error) {
	path = filepath.Clean(path)
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, nil, err
	}
	f, err := root.Open(filepath.Base(path))
	if err != nil {
		_ = root.Close()
		return nil, nil, err
	}
	return f, root, nil
}

// Grab captures rect using GNOME Shell's native screenshot service. A zero rect
// requests a full-screen capture; a non-empty rect uses ScreenshotArea.
func (b *GnomeShellScreenshotBackend) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	ctx = contextutil.Default(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("screen/gnome-shell: capture canceled: %w", ctxErr)
	}
	if b == nil || b.conn == nil || b.obj == nil {
		return nil, fmt.Errorf("screen/gnome-shell: backend is not initialized")
	}
	return b.grab(ctx, rect)
}

func (b *GnomeShellScreenshotBackend) grab(ctx context.Context, rect image.Rectangle) (img image.Image, retErr error) {
	if err := validateGnomeCaptureRect(rect); err != nil {
		return nil, err
	}
	tmp, err := newTempScreenshotFile("perfuncted-gnome-*.png")
	if err != nil {
		return nil, fmt.Errorf("screen/gnome-shell: temp file: %w", err)
	}
	path := tmp.Name()
	if closeErr := tmp.Close(); closeErr != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("screen/gnome-shell: close temp file: %w", closeErr)
	}
	defer func() {
		if err := removeScreenshotIfOwned(path, path); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("screen/gnome-shell: remove transport: %w", err))
		}
	}()

	var success bool
	var used string

	if rect.Empty() {
		call := b.obj.CallWithContext(ctx, gnomeShellShotIface+".Screenshot", 0, false, false, path)
		if call.Err != nil {
			return nil, fmt.Errorf("screen/gnome-shell: Screenshot: %w", call.Err)
		}
		err = call.Store(&success, &used)
		if err != nil {
			return nil, fmt.Errorf("screen/gnome-shell: Screenshot reply: %w", err)
		}
	} else {
		call := b.obj.CallWithContext(ctx, gnomeShellShotIface+".ScreenshotArea", 0,
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
	} else if used != path {
		defer func() {
			if err := os.Remove(used); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("screen/gnome-shell: remove compositor transport: %w", err))
			}
		}()
	}

	f, root, err := openScreenshotFile(used)
	if err != nil {
		return nil, fmt.Errorf("screen/gnome-shell: open %s: %w", used, err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("screen/gnome-shell: close screenshot: %w", err))
		}
		if err := root.Close(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("screen/gnome-shell: close screenshot root: %w", err))
		}
	}()

	img, err = png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("screen/gnome-shell: decode PNG: %w", err)
	}
	return img, nil
}

// Resolution returns the dimensions reported by GNOME Shell.
func (b *GnomeShellScreenshotBackend) Resolution() (int, int, error) {
	img, err := b.Grab(context.Background(), image.Rect(0, 0, 0, 0))
	if err != nil {
		return 0, 0, err
	}
	bounds := img.Bounds()
	return bounds.Dx(), bounds.Dy(), nil
}

// Close releases the GNOME Shell D-Bus connection.
func (b *GnomeShellScreenshotBackend) Close() error {
	if b == nil || b.conn == nil {
		return nil
	}
	return b.conn.Close()
}
