//go:build linux
// +build linux

package screen

import (
	"context"
	"fmt"
	"hash/crc32"
	"image"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/composite"
	"github.com/jezek/xgb/xproto"
)

// GrabFullHash returns a CRC32 checksum of the entire screen.
func (b *X11Backend) GrabFullHash(ctx context.Context) (uint32, error) {
	rect := image.Rect(0, 0, int(b.screen.WidthInPixels), int(b.screen.HeightInPixels))
	drawable := xproto.Drawable(b.root)

	if b.hasComposite {
		rawID, err := b.conn.NewId()
		if err == nil {
			pid := xproto.Pixmap(rawID)
			if composite.NameWindowPixmap(b.conn, b.root, pid).Check() == nil {
				drawable = xproto.Drawable(pid)
				defer xproto.FreePixmap(b.conn, pid) //nolint:errcheck
			}
		}
	}

	x := int16(rect.Min.X)
	y := int16(rect.Min.Y)
	w := uint16(rect.Dx())
	h := uint16(rect.Dy())
	planeMask := uint32(0xffffffff)

	reply, err := xproto.GetImage(b.conn, xproto.ImageFormatZPixmap,
		drawable, x, y, w, h, planeMask).Reply()
	if err != nil {
		return 0, fmt.Errorf("screen/x11: XGetImage: %w", err)
	}

	return crc32.ChecksumIEEE(reply.Data), nil
}

// GrabRegionHash returns a fast CRC32 hash for the specified rect. It uses
// XGetImage on the requested rectangle to avoid an intermediate image decode.
func (b *X11Backend) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	if rect.Empty() {
		return b.GrabFullHash(ctx)
	}

	drawable := xproto.Drawable(b.root)
	if b.hasComposite {
		rawID, err := b.conn.NewId()
		if err == nil {
			pid := xproto.Pixmap(rawID)
			if composite.NameWindowPixmap(b.conn, b.root, pid).Check() == nil {
				drawable = xproto.Drawable(pid)
				defer xproto.FreePixmap(b.conn, pid) //nolint:errcheck
			}
		}
	}

	x := int16(rect.Min.X)
	y := int16(rect.Min.Y)
	w := uint16(rect.Dx())
	h := uint16(rect.Dy())
	planeMask := uint32(0xffffffff)

	reply, err := xproto.GetImage(b.conn, xproto.ImageFormatZPixmap,
		drawable, x, y, w, h, planeMask).Reply()
	if err != nil {
		return 0, fmt.Errorf("screen/x11: XGetImage: %w", err)
	}

	return crc32.ChecksumIEEE(reply.Data), nil
}

// X11Backend captures screen regions via XGetImage on an X11 or XWayland display.
//
// On composited sessions (KDE/GNOME Wayland via XWayland) the root window is
// not directly readable with XGetImage. This backend automatically uses the
// Composite extension (NameWindowPixmap) when available, falling back to a
// direct root-window GetImage for plain X11 sessions.
//
// On Wayland, only X11/XWayland window content is visible through this path;
// native Wayland surfaces require Wayland capture protocols or portals.
type X11Backend struct {
	conn         *xgb.Conn
	root         xproto.Window
	screen       *xproto.ScreenInfo
	hasComposite bool
}

// NewX11Backend opens a connection to the X11 display specified by displayName
// (e.g. ":0"). Pass an empty string to use the DISPLAY environment variable.
func NewX11Backend(displayName string) (*X11Backend, error) {
	conn, err := xgb.NewConnDisplay(displayName)
	if err != nil {
		return nil, fmt.Errorf("screen/x11: connect to display %q: %w", displayName, err)
	}
	setup := xproto.Setup(conn)
	sc := setup.DefaultScreen(conn)
	b := &X11Backend{conn: conn, root: sc.Root, screen: sc}
	if composite.Init(conn) == nil {
		b.hasComposite = true
	}
	return b, nil
}

// Grab captures the pixels inside rect.
// On composited displays it snapshots the root via NameWindowPixmap so that
// the actual composited framebuffer is captured rather than the bare root pixmap.
func (b *X11Backend) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	drawable := xproto.Drawable(b.root)

	if b.hasComposite {
		// NameWindowPixmap gives us a pixmap containing the composited root
		// contents — necessary on XWayland where direct root GetImage fails.
		rawID, err := b.conn.NewId()
		if err == nil {
			pid := xproto.Pixmap(rawID)
			if composite.NameWindowPixmap(b.conn, b.root, pid).Check() == nil {
				drawable = xproto.Drawable(pid)
				defer xproto.FreePixmap(b.conn, pid) //nolint:errcheck
			}
		}
	}

	x := int16(rect.Min.X)
	y := int16(rect.Min.Y)
	w := uint16(rect.Dx())
	h := uint16(rect.Dy())
	planeMask := uint32(0xffffffff)

	reply, err := xproto.GetImage(b.conn, xproto.ImageFormatZPixmap,
		drawable, x, y, w, h, planeMask).Reply()
	if err != nil {
		return nil, fmt.Errorf("screen/x11: XGetImage: %w", err)
	}

	return decodeBGRA(reply.Data, int(w), int(h), int(w)*4), nil
}

// Close closes the X11 connection.
func (b *X11Backend) Close() error {
	b.conn.Close()
	return nil
}
