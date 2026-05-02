//go:build linux
// +build linux

package screen

import (
	"context"
	"hash/crc32"
	"image"
	"os"
	"strings"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// Mock connection for testing screen X11 backend.
type mockScreenConnection struct {
	defaultScreenFn    func() *xproto.ScreenInfo
	getImageFn         func(byte, xproto.Drawable, int16, int16, uint16, uint16, uint32) x11.GetImageCookie
	newIdFn            func() (uint32, error)
	freePixmapFn       func(xproto.Pixmap) x11.FreePixmapCookie
	initCompositeFn    func() error
	nameWindowPixmapFn func(xproto.Window, xproto.Pixmap) x11.NameWindowPixmapCookie
}

func (m *mockScreenConnection) Close() {}
func (m *mockScreenConnection) DefaultScreen() *xproto.ScreenInfo {
	if m.defaultScreenFn != nil {
		return m.defaultScreenFn()
	}
	return &xproto.ScreenInfo{Root: 1, WidthInPixels: 1920, HeightInPixels: 1080}
}
func (m *mockScreenConnection) InternAtom(bool, uint16, string) x11.InternAtomCookie {
	return &mockScreenInternAtomCookie{reply: &xproto.InternAtomReply{Atom: 1}}
}
func (m *mockScreenConnection) GetProperty(bool, xproto.Window, xproto.Atom, xproto.Atom, uint32, uint32) x11.GetPropertyCookie {
	return &mockScreenGetPropertyCookie{reply: &xproto.GetPropertyReply{Format: 32, Value: []byte{}}}
}
func (m *mockScreenConnection) GetGeometry(xproto.Drawable) x11.GetGeometryCookie {
	return &mockScreenGetGeometryCookie{reply: &xproto.GetGeometryReply{X: 0, Y: 0, Width: 100, Height: 100}}
}
func (m *mockScreenConnection) TranslateCoordinates(xproto.Window, xproto.Window, int16, int16) x11.TranslateCoordinatesCookie {
	return &mockScreenTranslateCoordinatesCookie{reply: &xproto.TranslateCoordinatesReply{DstX: 0, DstY: 0}}
}
func (m *mockScreenConnection) SendEventChecked(bool, xproto.Window, uint32, string) x11.SendEventCookie {
	return &mockScreenSendEventCookie{}
}
func (m *mockScreenConnection) MapWindowChecked(xproto.Window) x11.MapWindowCookie {
	return &mockScreenMapWindowCookie{}
}
func (m *mockScreenConnection) ConfigureWindowChecked(xproto.Window, uint16, []uint32) x11.ConfigureWindowCookie {
	return &mockScreenConfigureWindowCookie{}
}
func (m *mockScreenConnection) NewId() (uint32, error) {
	if m.newIdFn != nil {
		return m.newIdFn()
	}
	return 999, nil
}
func (m *mockScreenConnection) GetImage(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
	if m.getImageFn != nil {
		return m.getImageFn(format, drawable, x, y, width, height, planeMask)
	}
	data := make([]byte, 4*4)
	for i := range data {
		data[i] = byte(i + 1)
	}
	return &mockScreenGetImageCookie{reply: &xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}}
}
func (m *mockScreenConnection) FreePixmap(p xproto.Pixmap) x11.FreePixmapCookie {
	if m.freePixmapFn != nil {
		return m.freePixmapFn(p)
	}
	return &mockScreenFreePixmapCookie{}
}
func (m *mockScreenConnection) InitComposite() error {
	if m.initCompositeFn != nil {
		return m.initCompositeFn()
	}
	return nil
}
func (m *mockScreenConnection) NameWindowPixmap(w xproto.Window, p xproto.Pixmap) x11.NameWindowPixmapCookie {
	if m.nameWindowPixmapFn != nil {
		return m.nameWindowPixmapFn(w, p)
	}
	return &mockScreenNameWindowPixmapCookie{}
}

// Mock cookies for screen tests.
type mockScreenInternAtomCookie struct {
	reply *xproto.InternAtomReply
	err   error
}

func (m *mockScreenInternAtomCookie) Reply() (*xproto.InternAtomReply, error) {
	return m.reply, m.err
}

type mockScreenGetPropertyCookie struct {
	reply *xproto.GetPropertyReply
	err   error
}

func (m *mockScreenGetPropertyCookie) Reply() (*xproto.GetPropertyReply, error) {
	return m.reply, m.err
}

type mockScreenGetGeometryCookie struct {
	reply *xproto.GetGeometryReply
	err   error
}

func (m *mockScreenGetGeometryCookie) Reply() (*xproto.GetGeometryReply, error) {
	return m.reply, m.err
}

type mockScreenTranslateCoordinatesCookie struct {
	reply *xproto.TranslateCoordinatesReply
	err   error
}

func (m *mockScreenTranslateCoordinatesCookie) Reply() (*xproto.TranslateCoordinatesReply, error) {
	return m.reply, m.err
}

type mockScreenSendEventCookie struct{ err error }

func (m *mockScreenSendEventCookie) Check() error { return m.err }

type mockScreenMapWindowCookie struct{ err error }

func (m *mockScreenMapWindowCookie) Check() error { return m.err }

type mockScreenConfigureWindowCookie struct{ err error }

func (m *mockScreenConfigureWindowCookie) Check() error { return m.err }

type mockScreenGetImageCookie struct {
	reply *xproto.GetImageReply
	err   error
}

func (m *mockScreenGetImageCookie) Reply() (*xproto.GetImageReply, error) {
	if m.reply != nil {
		return m.reply, m.err
	}
	return &xproto.GetImageReply{}, m.err
}

type mockScreenFreePixmapCookie struct{ err error }

func (m *mockScreenFreePixmapCookie) Check() error { return m.err }

type mockScreenNameWindowPixmapCookie struct{ err error }

func (m *mockScreenNameWindowPixmapCookie) Check() error { return m.err }

// newStubScreenX11Backend creates a test X11Backend for screen capture.
func newStubScreenX11Backend(t *testing.T, hasComposite bool) *X11Backend {
	t.Helper()
	conn := &mockScreenConnection{}
	b := &X11Backend{
		conn:         conn,
		root:         1,
		screen:       &xproto.ScreenInfo{Root: 1, WidthInPixels: 1920, HeightInPixels: 1080},
		hasComposite: hasComposite,
	}
	conn.defaultScreenFn = func() *xproto.ScreenInfo {
		return b.screen
	}
	return b
}

func TestX11Backend_Grab(t *testing.T) {
	b := newStubScreenX11Backend(t, false)
	rect := image.Rect(0, 0, 2, 2)
	img, err := b.Grab(context.Background(), rect)
	if err != nil {
		t.Fatalf("Grab() unexpected error: %v", err)
	}
	if img.Bounds().Dx() != 2 || img.Bounds().Dy() != 2 {
		t.Errorf("Grab() image size = %dx%d, want 2x2", img.Bounds().Dx(), img.Bounds().Dy())
	}
}

func TestX11Backend_Grab_EmptyRect(t *testing.T) {
	b := newStubScreenX11Backend(t, false)
	// Empty rect should grab full screen - set up mock to return full screen data
	b.conn.(*mockScreenConnection).getImageFn = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		// Create data for full screen (1920x1080)
		data := make([]byte, int(width)*int(height)*4)
		for i := range data {
			data[i] = byte(i%255 + 1)
		}
		return &mockScreenGetImageCookie{reply: &xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}}
	}
	rect := image.Rect(0, 0, 0, 0)
	img, err := b.Grab(context.Background(), rect)
	if err != nil {
		t.Fatalf("Grab() unexpected error: %v", err)
	}
	if img.Bounds().Dx() == 0 || img.Bounds().Dy() == 0 {
		t.Errorf("Grab() with empty rect should grab full screen, got %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
	}
}

func TestX11Backend_Grab_WithComposite(t *testing.T) {
	var pixmapCreated bool
	b := newStubScreenX11Backend(t, true)
	b.conn.(*mockScreenConnection).newIdFn = func() (uint32, error) {
		return 100, nil
	}
	b.conn.(*mockScreenConnection).nameWindowPixmapFn = func(w xproto.Window, p xproto.Pixmap) x11.NameWindowPixmapCookie {
		pixmapCreated = true
		return &mockScreenNameWindowPixmapCookie{}
	}
	// Set up GetImage to return data for 100x100 image
	b.conn.(*mockScreenConnection).getImageFn = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		data := make([]byte, int(width)*int(height)*4)
		for i := range data {
			data[i] = byte(i%255 + 1)
		}
		return &mockScreenGetImageCookie{reply: &xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}}
	}
	rect := image.Rect(0, 0, 100, 100)
	img, err := b.Grab(context.Background(), rect)
	if err != nil {
		t.Fatalf("Grab() unexpected error: %v", err)
	}
	if img == nil {
		t.Fatal("Grab() returned nil image")
	}
	if !pixmapCreated {
		t.Error("Grab() with composite should have created a pixmap via NameWindowPixmap")
	}
}

func TestX11Backend_Grab_WithoutComposite(t *testing.T) {
	b := newStubScreenX11Backend(t, false)
	// Set up GetImage to return data for 100x100 image
	b.conn.(*mockScreenConnection).getImageFn = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		data := make([]byte, int(width)*int(height)*4)
		for i := range data {
			data[i] = byte(i%255 + 1)
		}
		return &mockScreenGetImageCookie{reply: &xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}}
	}
	rect := image.Rect(10, 20, 110, 120)
	img, err := b.Grab(context.Background(), rect)
	if err != nil {
		t.Fatalf("Grab() unexpected error: %v", err)
	}
	if img.Bounds().Dx() != 100 || img.Bounds().Dy() != 100 {
		t.Errorf("Grab() image size = %dx%d, want 100x100", img.Bounds().Dx(), img.Bounds().Dy())
	}
}

func TestX11Backend_Grab_Error(t *testing.T) {
	b := newStubScreenX11Backend(t, false)
	b.conn.(*mockScreenConnection).getImageFn = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return &mockScreenGetImageCookie{err: image.ErrFormat}
	}
	rect := image.Rect(0, 0, 100, 100)
	_, err := b.Grab(context.Background(), rect)
	if err == nil || !strings.Contains(err.Error(), "XGetImage") {
		t.Errorf("Grab() error = %v, want error containing %q", err, "XGetImage")
	}
}

func TestX11Backend_GrabFullHash(t *testing.T) {
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	expectedHash := crc32.ChecksumIEEE(data)

	b := newStubScreenX11Backend(t, false)
	b.conn.(*mockScreenConnection).getImageFn = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return &mockScreenGetImageCookie{reply: &xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}}
	}
	hash, err := b.GrabFullHash(context.Background())
	if err != nil {
		t.Fatalf("GrabFullHash() unexpected error: %v", err)
	}
	if hash != expectedHash {
		t.Errorf("GrabFullHash() = %d, want %d", hash, expectedHash)
	}
}

func TestX11Backend_GrabFullHash_WithComposite(t *testing.T) {
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	expectedHash := crc32.ChecksumIEEE(data)

	b := newStubScreenX11Backend(t, true)
	b.conn.(*mockScreenConnection).newIdFn = func() (uint32, error) {
		return 100, nil
	}
	b.conn.(*mockScreenConnection).getImageFn = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return &mockScreenGetImageCookie{reply: &xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}}
	}
	hash, err := b.GrabFullHash(context.Background())
	if err != nil {
		t.Fatalf("GrabFullHash() unexpected error: %v", err)
	}
	if hash != expectedHash {
		t.Errorf("GrabFullHash() = %d, want %d", hash, expectedHash)
	}
}

func TestX11Backend_GrabRegionHash(t *testing.T) {
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	expectedHash := crc32.ChecksumIEEE(data)

	b := newStubScreenX11Backend(t, false)
	b.conn.(*mockScreenConnection).getImageFn = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return &mockScreenGetImageCookie{reply: &xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}}
	}
	rect := image.Rect(0, 0, 2, 2)
	hash, err := b.GrabRegionHash(context.Background(), rect)
	if err != nil {
		t.Fatalf("GrabRegionHash() unexpected error: %v", err)
	}
	if hash != expectedHash {
		t.Errorf("GrabRegionHash() = %d, want %d", hash, expectedHash)
	}
}

func TestX11Backend_GrabRegionHash_EmptyRect(t *testing.T) {
	b := newStubScreenX11Backend(t, false)
	hash1, _ := b.GrabFullHash(context.Background())
	hash2, err := b.GrabRegionHash(context.Background(), image.Rect(0, 0, 0, 0))
	if err != nil {
		t.Fatalf("GrabRegionHash() unexpected error: %v", err)
	}
	if hash1 != hash2 {
		t.Errorf("GrabRegionHash(empty rect) = %d, want %d (same as GrabFullHash)", hash2, hash1)
	}
}

func TestX11Backend_GrabRegionHash_WithComposite(t *testing.T) {
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	expectedHash := crc32.ChecksumIEEE(data)

	b := newStubScreenX11Backend(t, true)
	b.conn.(*mockScreenConnection).newIdFn = func() (uint32, error) {
		return 100, nil
	}
	b.conn.(*mockScreenConnection).getImageFn = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return &mockScreenGetImageCookie{reply: &xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}}
	}
	rect := image.Rect(10, 20, 110, 120)
	hash, err := b.GrabRegionHash(context.Background(), rect)
	if err != nil {
		t.Fatalf("GrabRegionHash() unexpected error: %v", err)
	}
	if hash != expectedHash {
		t.Errorf("GrabRegionHash() = %d, want %d", hash, expectedHash)
	}
}

func TestX11Backend_New_NoDisplay(t *testing.T) {
	orig := os.Getenv("DISPLAY")
	os.Unsetenv("DISPLAY")
	defer os.Setenv("DISPLAY", orig)
	_, err := NewX11Backend("")
	if err == nil || !strings.Contains(err.Error(), "screen/x11: connect to display") {
		t.Fatalf("NewX11Backend() error = %v, want connection error", err)
	}
}

func TestX11Backend_Close(t *testing.T) {
	b := newStubScreenX11Backend(t, false)
	if err := b.Close(); err != nil {
		t.Errorf("Close() unexpected error: %v", err)
	}
}

func TestX11Backend_Grab_ColorCheck(t *testing.T) {
	data := []byte{1, 2, 3, 255}
	b := newStubScreenX11Backend(t, false)
	b.conn.(*mockScreenConnection).getImageFn = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return &mockScreenGetImageCookie{reply: &xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}}
	}
	rect := image.Rect(0, 0, 1, 1)
	img, err := b.Grab(context.Background(), rect)
	if err != nil {
		t.Fatalf("Grab() unexpected error: %v", err)
	}
	rgba, ok := img.(*image.RGBA)
	if !ok {
		t.Fatal("Grab() did not return *image.RGBA")
	}
	c := rgba.RGBAAt(0, 0)
	if c.R != 3 || c.G != 2 || c.B != 1 || c.A != 255 {
		t.Errorf("Grab() pixel = RGBA(%d,%d,%d,%d), want RGBA(3,2,1,255)", c.R, c.G, c.B, c.A)
	}
}

func TestX11Backend_Resolution(t *testing.T) {
	b := newStubScreenX11Backend(t, false)
	w, h, err := Resolution(b)
	if err != nil {
		t.Fatalf("Resolution() unexpected error: %v", err)
	}
	if w != 1920 || h != 1080 {
		t.Errorf("Resolution() = %dx%d, want 1920x1080", w, h)
	}
}

func TestX11Backend_ImplementsScreenshotter(t *testing.T) {
	b := newStubScreenX11Backend(t, false)
	var _ Screenshotter = b
}

func TestX11Backend_ImplementsResolver(t *testing.T) {
	b := newStubScreenX11Backend(t, false)
	var _ Resolver = b
}

func TestDecodeBGRA_Integration(t *testing.T) {
	b := newStubScreenX11Backend(t, false)
	data := []byte{
		10, 20, 30, 255,
		40, 50, 60, 255,
		70, 80, 90, 255,
		100, 110, 120, 255,
	}
	b.conn.(*mockScreenConnection).getImageFn = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return &mockScreenGetImageCookie{reply: &xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}}
	}
	rect := image.Rect(0, 0, 2, 2)
	img, err := b.Grab(context.Background(), rect)
	if err != nil {
		t.Fatalf("Grab() unexpected error: %v", err)
	}
	rgba, ok := img.(*image.RGBA)
	if !ok {
		t.Fatal("Grab() did not return *image.RGBA")
	}
	tests := []struct {
		x, y    int
		r, g, b uint8
	}{
		{0, 0, 30, 20, 10},
		{1, 0, 60, 50, 40},
		{0, 1, 90, 80, 70},
		{1, 1, 120, 110, 100},
	}
	for _, tc := range tests {
		c := rgba.RGBAAt(tc.x, tc.y)
		if c.R != tc.r || c.G != tc.g || c.B != tc.b {
			t.Errorf("(%d,%d): got RGB(%d,%d,%d) want RGB(%d,%d,%d)",
				tc.x, tc.y, c.R, c.G, c.B, tc.r, tc.g, tc.b)
		}
	}
}

func TestX11Backend_GrabFullHash_Error(t *testing.T) {
	b := newStubScreenX11Backend(t, false)
	b.conn.(*mockScreenConnection).getImageFn = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return &mockScreenGetImageCookie{err: image.ErrFormat}
	}
	_, err := b.GrabFullHash(context.Background())
	if err == nil || !strings.Contains(err.Error(), "XGetImage") {
		t.Errorf("GrabFullHash() error = %v, want error containing %q", err, "XGetImage")
	}
}

func TestX11Backend_GrabRegionHash_Error(t *testing.T) {
	b := newStubScreenX11Backend(t, false)
	b.conn.(*mockScreenConnection).getImageFn = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return &mockScreenGetImageCookie{err: image.ErrFormat}
	}
	rect := image.Rect(0, 0, 100, 100)
	_, err := b.GrabRegionHash(context.Background(), rect)
	if err == nil || !strings.Contains(err.Error(), "XGetImage") {
		t.Errorf("GrabRegionHash() error = %v, want error containing %q", err, "XGetImage")
	}
}

func TestX11Backend_Grab_ImageCheck(t *testing.T) {
	b := newStubScreenX11Backend(t, false)
	data := make([]byte, 4*4*4)
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			off := (y*2 + x) * 4
			data[off] = byte(x * 10)
			data[off+1] = byte(y * 10)
			data[off+2] = byte(x * y * 10)
			data[off+3] = 255
		}
	}
	b.conn.(*mockScreenConnection).getImageFn = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return &mockScreenGetImageCookie{reply: &xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}}
	}
	rect := image.Rect(0, 0, 2, 2)
	img, err := b.Grab(context.Background(), rect)
	if err != nil {
		t.Fatalf("Grab() unexpected error: %v", err)
	}
	if img == nil {
		t.Fatal("Grab() returned nil image")
	}
	rgba, ok := img.(*image.RGBA)
	if !ok {
		t.Fatal("Grab() did not return *image.RGBA")
	}
	if rgba.Bounds().Dx() != 2 || rgba.Bounds().Dy() != 2 {
		t.Errorf("Grab() image size = %dx%d, want 2x2", rgba.Bounds().Dx(), rgba.Bounds().Dy())
	}
	c := rgba.RGBAAt(1, 1)
	if c.R != 10 || c.G != 10 || c.B != 10 {
		t.Errorf("Grab() pixel (1,1) = RGB(%d,%d,%d), want RGB(10,10,10)", c.R, c.G, c.B)
	}
}

func TestX11Backend_Grab_NilImage(t *testing.T) {
	b := newStubScreenX11Backend(t, false)
	b.conn.(*mockScreenConnection).getImageFn = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return &mockScreenGetImageCookie{reply: &xproto.GetImageReply{Depth: 24, Visual: 1, Data: nil}}
	}
	rect := image.Rect(0, 0, 10, 10)
	img, err := b.Grab(context.Background(), rect)
	if err != nil {
		t.Fatalf("Grab() unexpected error: %v", err)
	}
	// Nil data should result in an empty image
	if img == nil {
		t.Fatal("Grab() returned nil image for nil data")
	}
}
