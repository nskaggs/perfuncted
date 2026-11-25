//go:build linux
// +build linux

package screen

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"net"
	"strconv"
	"sync"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/internal/contextutil"
	"github.com/nskaggs/perfuncted/internal/x11"
)

var _ Screenshotter = (*X11Backend)(nil)

// ErrBackgroundWindowCaptureUnsupported indicates that X11 cannot provide an
// authoritative, already-composited window pixmap for the requested target.
var ErrBackgroundWindowCaptureUnsupported = errors.New("screen/x11: background window capture unsupported")

// ErrBackgroundWindowUnmapped identifies a target window that is not mapped
// and viewable on the X11 screen.
var ErrBackgroundWindowUnmapped = errors.New("screen/x11: target window is unmapped or unviewable")

// WindowPixmapCapture is a target-only X11 window-buffer capture. Visibility
// is conservative: an overlapping mapped top-level window bounding box is
// reported as occluded; exact shaped/translucent coverage is not inferred.
type WindowPixmapCapture struct {
	Image           image.Image
	ScreenRect      image.Rectangle
	Visibility      WindowPixmapVisibility
	Foreground      bool
	ForegroundKnown bool
}

// WindowPixmapVisibility describes what the X11 window tree can establish
// about the target's screen position at capture time.
type WindowPixmapVisibility string

const (
	// WindowPixmapUnobscured means no mapped root-level sibling overlaps the target bounds.
	WindowPixmapUnobscured WindowPixmapVisibility = "unobscured"
	// WindowPixmapOccluded means at least one mapped root-level sibling overlaps the target bounds.
	WindowPixmapOccluded WindowPixmapVisibility = "occluded"
	// WindowPixmapUnknown means visibility could not be established conservatively.
	WindowPixmapUnknown WindowPixmapVisibility = "unknown"
)

type windowTreeConnection interface {
	QueryTree(xproto.Window) x11.QueryTreeCookie
	GetWindowAttributes(xproto.Window) x11.GetWindowAttributesCookie
}

// CaptureWindowPixmap reads the target's own Composite named pixmap. It never
// redirects a window or falls back to a crop of the visible desktop. A
// compositor must already have redirected the mapped target.
func (b *X11Backend) CaptureWindowPixmap(ctx context.Context, stableID string) (WindowPixmapCapture, error) {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return WindowPixmapCapture{}, fmt.Errorf("screen/x11: background capture canceled: %w", err)
	}
	finish, err := b.beginOperation()
	if err != nil {
		return WindowPixmapCapture{}, err
	}
	defer finish()
	if !b.hasComposite {
		return WindowPixmapCapture{}, ErrBackgroundWindowCaptureUnsupported
	}
	tree, ok := b.conn.(windowTreeConnection)
	if !ok {
		return WindowPixmapCapture{}, ErrBackgroundWindowCaptureUnsupported
	}
	parsedID, err := strconv.ParseUint(stableID, 10, 32)
	if err != nil || parsedID == 0 {
		return WindowPixmapCapture{}, fmt.Errorf("screen/x11: invalid stable X11 window ID %q: %w", stableID, ErrBackgroundWindowCaptureUnsupported)
	}
	windowID := xproto.Window(parsedID)

	geometry, rootRect, err := b.targetPixmapGeometry(tree, windowID)
	if err != nil {
		return WindowPixmapCapture{}, err
	}
	width, height := int(geometry.Width), int(geometry.Height)

	data, err := b.readTargetPixmap(windowID, geometry)
	if err != nil {
		return WindowPixmapCapture{}, err
	}

	if err := ctx.Err(); err != nil {
		return WindowPixmapCapture{}, fmt.Errorf("screen/x11: background capture canceled: %w", err)
	}
	visibility := b.windowPixmapVisibility(tree, windowID, rootRect)
	foreground, foregroundKnown := b.windowIsForeground(windowID)
	return WindowPixmapCapture{
		Image:           decodeBGRA(data, width, height, width*4),
		ScreenRect:      rootRect,
		Visibility:      visibility,
		Foreground:      foreground,
		ForegroundKnown: foregroundKnown,
	}, nil
}

func (b *X11Backend) windowIsForeground(target xproto.Window) (bool, bool) {
	if b.activeAtom == xproto.AtomNone {
		return false, false
	}
	reply, err := b.conn.GetProperty(false, b.root, b.activeAtom, xproto.AtomWindow, 0, 1).Reply()
	if err != nil || reply == nil || reply.Format != 32 || reply.BytesAfter != 0 || len(reply.Value) != 4 {
		return false, false
	}
	active := xproto.Window(uint32(reply.Value[0]) | uint32(reply.Value[1])<<8 |
		uint32(reply.Value[2])<<16 | uint32(reply.Value[3])<<24)
	return active == target, true
}

func (b *X11Backend) windowPixmapVisibility(tree windowTreeConnection, target xproto.Window, targetRect image.Rectangle) WindowPixmapVisibility {
	rootBounds := image.Rect(0, 0, int(b.screen.WidthInPixels), int(b.screen.HeightInPixels))
	if !targetRect.In(rootBounds) {
		return WindowPixmapUnknown
	}
	rootChild, ok := b.rootChildForPixmap(tree, target)
	if !ok {
		return WindowPixmapUnknown
	}
	rootTree, err := tree.QueryTree(b.root).Reply()
	if err != nil || rootTree == nil {
		return WindowPixmapUnknown
	}
	index := -1
	for i, child := range rootTree.Children {
		if child == rootChild {
			index = i
			break
		}
	}
	if index < 0 {
		return WindowPixmapUnknown
	}
	return b.siblingPixmapVisibility(tree, rootTree.Children[index+1:], targetRect)
}

func unsupportedPixmap(reason string, cause error) error {
	wrapped := fmt.Errorf("screen/x11: %s", reason)
	if cause != nil {
		wrapped = errors.Join(wrapped, cause)
	}
	return errors.Join(ErrBackgroundWindowCaptureUnsupported, wrapped)
}

// GrabFullHash returns a CRC32 checksum of the entire screen.
func (b *X11Backend) GrabFullHash(ctx context.Context) (uint32, error) {
	ctx = contextutil.Default(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, fmt.Errorf("screen/x11: capture canceled: %w", ctxErr)
	}
	finish, err := b.beginOperation()
	if err != nil {
		return 0, err
	}
	defer finish()
	rect := image.Rect(0, 0, int(b.screen.WidthInPixels), int(b.screen.HeightInPixels))
	if rectErr := validateX11Rect(rect); rectErr != nil {
		return 0, rectErr
	}
	drawable, free := b.compositeDrawableForRoot()
	defer free()

	x := int16(rect.Min.X)
	y := int16(rect.Min.Y)
	w := uint16(rect.Dx())
	h := uint16(rect.Dy())
	planeMask := uint32(0xffffffff)

	reply, err := b.conn.GetImage(xproto.ImageFormatZPixmap,
		drawable, x, y, w, h, planeMask).Reply()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, fmt.Errorf("screen/x11: capture canceled: %w", ctxErr)
	}
	if err != nil {
		return 0, fmt.Errorf("screen/x11: XGetImage: %w", err)
	}
	if err := validateX11PixelBuffer(reply.Data, int(w), int(h)); err != nil {
		return 0, err
	}

	return crc32.ChecksumIEEE(reply.Data), nil
}

// GrabRegionHash returns a fast CRC32 hash for the specified rect. It uses
// XGetImage on the requested rectangle to avoid an intermediate image decode.
func (b *X11Backend) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	ctx = contextutil.Default(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, fmt.Errorf("screen/x11: capture canceled: %w", ctxErr)
	}
	if rect != (image.Rectangle{}) && rect.Empty() {
		return 0, fmt.Errorf("screen/x11: invalid empty capture region %v", rect)
	}
	if rect.Empty() {
		return b.GrabFullHash(ctx)
	}
	finish, err := b.beginOperation()
	if err != nil {
		return 0, err
	}
	defer finish()
	if rectErr := validateX11Rect(rect); rectErr != nil {
		return 0, rectErr
	}

	drawable, free := b.compositeDrawableForRoot()
	defer free()

	x := int16(rect.Min.X)
	y := int16(rect.Min.Y)
	w := uint16(rect.Dx())
	h := uint16(rect.Dy())
	planeMask := uint32(0xffffffff)

	reply, err := b.conn.GetImage(xproto.ImageFormatZPixmap,
		drawable, x, y, w, h, planeMask).Reply()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, fmt.Errorf("screen/x11: capture canceled: %w", ctxErr)
	}
	if err != nil {
		return 0, fmt.Errorf("screen/x11: XGetImage: %w", err)
	}
	if err := validateX11PixelBuffer(reply.Data, int(w), int(h)); err != nil {
		return 0, err
	}

	return crc32.ChecksumIEEE(reply.Data), nil
}

// X11Backend captures screen regions via XGetImage on an X11 or XWayland display.

func (b *X11Backend) compositeDrawableForRoot() (xproto.Drawable, func()) {
	drawable := xproto.Drawable(b.root)
	if b.hasComposite {
		rawID, err := b.conn.NewID()
		if err == nil {
			pid := xproto.Pixmap(rawID)
			if b.conn.NameWindowPixmap(b.root, pid).Check() == nil {
				return xproto.Drawable(pid), func() { _ = b.conn.FreePixmap(pid) } //nolint:errcheck
			}
		}
	}
	return drawable, func() {}
}

// X11Backend captures pixels from an X11 or XWayland display. On composited
// sessions (KDE/GNOME Wayland via XWayland) the root window is
// not directly readable with XGetImage. This backend automatically uses the
// Composite extension (NameWindowPixmap) when available, falling back to a
// direct root-window GetImage for plain X11 sessions.
//
// On Wayland, only X11/XWayland window content is visible through this path;
// native Wayland surfaces require Wayland capture protocols or portals.
type X11Backend struct {
	conn         x11.Connection
	root         xproto.Window
	screen       *xproto.ScreenInfo
	hasComposite bool
	activeAtom   xproto.Atom

	lifecycleMu sync.Mutex
	closed      bool
	active      int
	activeDone  chan struct{}
	closeOnce   sync.Once
}

// NewX11Backend opens a connection to the X11 display specified by displayName
// (e.g. ":0"). Pass an empty string to use the DISPLAY environment variable.
func NewX11Backend(displayName string) (*X11Backend, error) {
	conn, err := x11.NewXgbConnection(displayName)
	if err != nil {
		return nil, fmt.Errorf("screen/x11: connect to display %q: %w", displayName, err)
	}
	sc := conn.DefaultScreen()
	b := &X11Backend{conn: conn, root: sc.Root, screen: sc}
	if b.conn.InitComposite() == nil {
		b.hasComposite = true
	}
	if active, atomErr := b.conn.InternAtom(true, uint16(len("_NET_ACTIVE_WINDOW")), "_NET_ACTIVE_WINDOW").Reply(); atomErr == nil && active != nil {
		b.activeAtom = active.Atom
	}
	return b, nil
}

// Grab captures the pixels inside rect.
// On composited displays it snapshots the root via NameWindowPixmap so that
// the actual composited framebuffer is captured rather than the bare root pixmap.
func (b *X11Backend) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	ctx = contextutil.Default(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("screen/x11: capture canceled: %w", ctxErr)
	}
	if rect != (image.Rectangle{}) && rect.Empty() {
		return nil, fmt.Errorf("screen/x11: invalid empty capture region %v", rect)
	}
	finish, err := b.beginOperation()
	if err != nil {
		return nil, err
	}
	defer finish()
	if rect.Empty() {
		rect = image.Rect(0, 0, int(b.screen.WidthInPixels), int(b.screen.HeightInPixels))
	}
	if rectErr := validateX11Rect(rect); rectErr != nil {
		return nil, rectErr
	}
	drawable, free := b.compositeDrawableForRoot()
	defer free()

	x := int16(rect.Min.X)
	y := int16(rect.Min.Y)
	w := uint16(rect.Dx())
	h := uint16(rect.Dy())
	planeMask := uint32(0xffffffff)

	reply, err := b.conn.GetImage(xproto.ImageFormatZPixmap,
		drawable, x, y, w, h, planeMask).Reply()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("screen/x11: capture canceled: %w", ctxErr)
	}
	if err != nil {
		return nil, fmt.Errorf("screen/x11: XGetImage: %w", err)
	}
	if err := validateX11PixelBuffer(reply.Data, int(w), int(h)); err != nil {
		return nil, err
	}

	return decodeBGRA(reply.Data, int(w), int(h), int(w)*4), nil
}

// GrabVerified proves that the requested root-window rectangle is contained
// in the X11 screen and that XGetImage returned its exact pixel dimensions.
func (b *X11Backend) GrabVerified(ctx context.Context, rect image.Rectangle) (find.VerifiedTemplateCapture, error) {
	if b == nil || b.screen == nil || rect.Empty() {
		return find.VerifiedTemplateCapture{}, fmt.Errorf("%w: invalid X11 screen or empty region %v", find.ErrUnverifiedCaptureGeometry, rect)
	}
	if !rect.In(image.Rect(0, 0, int(b.screen.WidthInPixels), int(b.screen.HeightInPixels))) {
		return find.VerifiedTemplateCapture{}, fmt.Errorf("%w: X11 region %v is outside screen bounds", find.ErrUnverifiedCaptureGeometry, rect)
	}
	img, err := b.Grab(ctx, rect)
	if err != nil {
		return find.VerifiedTemplateCapture{}, err
	}
	bounds := img.Bounds()
	if bounds.Dx() != rect.Dx() || bounds.Dy() != rect.Dy() {
		return find.VerifiedTemplateCapture{}, fmt.Errorf("%w: X11 returned %v for requested region %v", find.ErrUnverifiedCaptureGeometry, bounds, rect)
	}
	return find.VerifiedTemplateCapture{Image: img, DesktopRect: rect, PixelScale: 1}, nil
}

// Resolution returns the screen dimensions from the X11 screen info.
func (b *X11Backend) Resolution() (int, int, error) {
	finish, err := b.beginOperation()
	if err != nil {
		return 0, 0, err
	}
	defer finish()
	return int(b.screen.WidthInPixels), int(b.screen.HeightInPixels), nil
}

// Close closes the X11 connection.
func (b *X11Backend) Close() error {
	if b == nil {
		return nil
	}
	b.closeOnce.Do(func() {
		b.lifecycleMu.Lock()
		b.closed = true
		activeDone := b.activeDone
		conn := b.conn
		b.lifecycleMu.Unlock()

		if conn != nil {
			conn.Close()
		}
		if activeDone != nil {
			<-activeDone
		}
	})
	return nil
}

func (b *X11Backend) beginOperation() (func(), error) {
	if b == nil {
		return nil, fmt.Errorf("screen/x11: backend is nil")
	}
	b.lifecycleMu.Lock()
	defer b.lifecycleMu.Unlock()
	if b.closed {
		return nil, fmt.Errorf("screen/x11: backend is closed: %w", net.ErrClosed)
	}
	if b.conn == nil || b.screen == nil {
		return nil, fmt.Errorf("screen/x11: backend is not initialized")
	}
	if b.active == 0 {
		b.activeDone = make(chan struct{})
	}
	b.active++

	return sync.OnceFunc(func() {
		b.lifecycleMu.Lock()
		b.active--
		if b.active == 0 {
			close(b.activeDone)
			b.activeDone = nil
		}
		b.lifecycleMu.Unlock()
	}), nil
}

func validateX11PixelBuffer(data []byte, w, h int) error {
	if w <= 0 || h <= 0 {
		return fmt.Errorf("screen/x11: invalid pixel dimensions %dx%d", w, h)
	}
	expected := uint64(w) * uint64(h) * 4
	if expected > uint64(len(data)) {
		return fmt.Errorf("screen/x11: short pixel buffer: got %d bytes, want %d", len(data), expected)
	}
	return nil
}

func validateX11Rect(rect image.Rectangle) error {
	if rect.Empty() {
		return fmt.Errorf("screen/x11: empty capture rectangle")
	}
	if rect.Min.X < -32768 || rect.Min.X > 32767 || rect.Min.Y < -32768 || rect.Min.Y > 32767 {
		return fmt.Errorf("screen/x11: rectangle origin out of range: %v", rect)
	}
	const maxDimension = int(^uint16(0))
	if rect.Max.X > rect.Min.X+maxDimension || rect.Max.Y > rect.Min.Y+maxDimension {
		return fmt.Errorf("screen/x11: rectangle dimensions exceed X11 limits: %v", rect)
	}
	return nil
}

func (b *X11Backend) targetPixmapGeometry(tree windowTreeConnection, windowID xproto.Window) (*xproto.GetGeometryReply, image.Rectangle, error) {
	attrs, err := tree.GetWindowAttributes(windowID).Reply()
	if err != nil || attrs == nil || attrs.MapState != xproto.MapStateViewable {
		return nil, image.Rectangle{}, errors.Join(ErrBackgroundWindowCaptureUnsupported, ErrBackgroundWindowUnmapped, unsupportedPixmap("target window is not mapped and viewable", err))
	}
	geometry, err := b.conn.GetGeometry(xproto.Drawable(windowID)).Reply()
	if err != nil || geometry == nil || geometry.Width == 0 || geometry.Height == 0 || geometry.Depth != b.screen.RootDepth {
		return nil, image.Rectangle{}, unsupportedPixmap("target window geometry is unavailable or incompatible", err)
	}
	translated, err := b.conn.TranslateCoordinates(windowID, b.root, 0, 0).Reply()
	if err != nil || translated == nil || !translated.SameScreen {
		return nil, image.Rectangle{}, unsupportedPixmap("target window root coordinates are unavailable", err)
	}
	width, height := int(geometry.Width), int(geometry.Height)
	rootRect := image.Rect(int(translated.DstX), int(translated.DstY), int(translated.DstX)+width, int(translated.DstY)+height)
	if rootRect.Empty() || rootRect.Dx() > 65535 || rootRect.Dy() > 65535 {
		return nil, image.Rectangle{}, ErrBackgroundWindowCaptureUnsupported
	}
	return geometry, rootRect, nil
}

func (b *X11Backend) readTargetPixmap(windowID xproto.Window, geometry *xproto.GetGeometryReply) ([]byte, error) {
	pixmapID, err := b.conn.NewID()
	if err != nil {
		return nil, fmt.Errorf("screen/x11: allocate target pixmap: %w", err)
	}
	pixmap := xproto.Pixmap(pixmapID)
	if nameErr := b.conn.NameWindowPixmap(windowID, pixmap).Check(); nameErr != nil {
		return nil, unsupportedPixmap("target has no already-redirected Composite pixmap", nameErr)
	}
	defer func() { _ = b.conn.FreePixmap(pixmap).Check() }()
	pixmapGeometry, err := b.conn.GetGeometry(xproto.Drawable(pixmap)).Reply()
	if err != nil || pixmapGeometry == nil || pixmapGeometry.Width != geometry.Width || pixmapGeometry.Height != geometry.Height || pixmapGeometry.Depth != geometry.Depth {
		return nil, unsupportedPixmap("target pixmap geometry changed or is incompatible", err)
	}
	reply, err := b.conn.GetImage(xproto.ImageFormatZPixmap, xproto.Drawable(pixmap), 0, 0, geometry.Width, geometry.Height, 0xffffffff).Reply()
	if err != nil || reply == nil || reply.Depth != geometry.Depth {
		if err == nil {
			err = fmt.Errorf("unexpected image depth")
		}
		return nil, fmt.Errorf("screen/x11: read target pixmap: %w", err)
	}
	if err := validateX11PixelBuffer(reply.Data, int(geometry.Width), int(geometry.Height)); err != nil {
		return nil, err
	}
	return reply.Data, nil
}

func (b *X11Backend) rootChildForPixmap(tree windowTreeConnection, target xproto.Window) (xproto.Window, bool) {
	rootChild := target
	for depth := 0; depth < 64; depth++ {
		reply, err := tree.QueryTree(rootChild).Reply()
		if err != nil || reply == nil || reply.Parent == xproto.WindowNone {
			return 0, false
		}
		if reply.Parent == b.root {
			return rootChild, true
		}
		rootChild = reply.Parent
	}
	return 0, false
}

func (b *X11Backend) siblingPixmapVisibility(tree windowTreeConnection, siblings []xproto.Window, targetRect image.Rectangle) WindowPixmapVisibility {
	for _, sibling := range siblings {
		attrs, err := tree.GetWindowAttributes(sibling).Reply()
		if err != nil || attrs == nil {
			return WindowPixmapUnknown
		}
		if attrs.MapState != xproto.MapStateViewable {
			continue
		}
		geometry, err := b.conn.GetGeometry(xproto.Drawable(sibling)).Reply()
		if err != nil || geometry == nil || geometry.Width == 0 || geometry.Height == 0 {
			return WindowPixmapUnknown
		}
		translated, err := b.conn.TranslateCoordinates(sibling, b.root, 0, 0).Reply()
		if err != nil || translated == nil || !translated.SameScreen {
			return WindowPixmapUnknown
		}
		siblingRect := image.Rect(int(translated.DstX), int(translated.DstY), int(translated.DstX)+int(geometry.Width), int(translated.DstY)+int(geometry.Height))
		if !siblingRect.Intersect(targetRect).Empty() {
			return WindowPixmapOccluded
		}
	}
	return WindowPixmapUnobscured
}
