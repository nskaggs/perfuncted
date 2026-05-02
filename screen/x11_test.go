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

// newStubScreenX11Backend creates a test X11Backend backed by the shared
// x11.MockConnection so all screen tests use the same mock infrastructure.
func newStubScreenX11Backend(t *testing.T, hasComposite bool) *X11Backend {
	t.Helper()
	screenInfo := &xproto.ScreenInfo{Root: 1, WidthInPixels: 1920, HeightInPixels: 1080}
	mc := &x11.MockConnection{}
	mc.DefaultScreenFunc = func() *xproto.ScreenInfo { return screenInfo }
	// Provide a default GetImage that returns a small non-empty image so tests
	// that don't override it still get a valid (non-error) path.
	mc.GetImageFunc = func(_ byte, _ xproto.Drawable, _, _ int16, w, h uint16, _ uint32) x11.GetImageCookie {
		data := make([]byte, int(w)*int(h)*4)
		for i := range data {
			data[i] = byte(i + 1)
		}
		return x11.NewMockGetImageCookie(&xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}, nil)
	}
	return &X11Backend{conn: mc, root: 1, screen: screenInfo, hasComposite: hasComposite}
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
	b.conn.(*x11.MockConnection).GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		// Create data for full screen (1920x1080)
		data := make([]byte, int(width)*int(height)*4)
		for i := range data {
			data[i] = byte(i%255 + 1)
		}
		return x11.NewMockGetImageCookie(&xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}, nil)
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
	b.conn.(*x11.MockConnection).NewIdFunc = func() (uint32, error) {
		return 100, nil
	}
	b.conn.(*x11.MockConnection).NameWindowPixmapFunc = func(w xproto.Window, p xproto.Pixmap) x11.NameWindowPixmapCookie {
		pixmapCreated = true
		return &x11.MockCheckCookie{}
	}
	// Set up GetImage to return data for 100x100 image
	b.conn.(*x11.MockConnection).GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		data := make([]byte, int(width)*int(height)*4)
		for i := range data {
			data[i] = byte(i%255 + 1)
		}
		return x11.NewMockGetImageCookie(&xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}, nil)
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
	b.conn.(*x11.MockConnection).GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		data := make([]byte, int(width)*int(height)*4)
		for i := range data {
			data[i] = byte(i%255 + 1)
		}
		return x11.NewMockGetImageCookie(&xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}, nil)
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
	b.conn.(*x11.MockConnection).GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return x11.NewMockGetImageCookie(nil, image.ErrFormat)
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
	b.conn.(*x11.MockConnection).GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return x11.NewMockGetImageCookie(&xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}, nil)
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
	b.conn.(*x11.MockConnection).NewIdFunc = func() (uint32, error) {
		return 100, nil
	}
	b.conn.(*x11.MockConnection).GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return x11.NewMockGetImageCookie(&xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}, nil)
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
	b.conn.(*x11.MockConnection).GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return x11.NewMockGetImageCookie(&xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}, nil)
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
	b.conn.(*x11.MockConnection).NewIdFunc = func() (uint32, error) {
		return 100, nil
	}
	b.conn.(*x11.MockConnection).GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return x11.NewMockGetImageCookie(&xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}, nil)
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
	b.conn.(*x11.MockConnection).GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return x11.NewMockGetImageCookie(&xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}, nil)
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
	b.conn.(*x11.MockConnection).GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return x11.NewMockGetImageCookie(&xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}, nil)
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
	b.conn.(*x11.MockConnection).GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return x11.NewMockGetImageCookie(nil, image.ErrFormat)
	}
	_, err := b.GrabFullHash(context.Background())
	if err == nil || !strings.Contains(err.Error(), "XGetImage") {
		t.Errorf("GrabFullHash() error = %v, want error containing %q", err, "XGetImage")
	}
}

func TestX11Backend_GrabRegionHash_Error(t *testing.T) {
	b := newStubScreenX11Backend(t, false)
	b.conn.(*x11.MockConnection).GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return x11.NewMockGetImageCookie(nil, image.ErrFormat)
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
	b.conn.(*x11.MockConnection).GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return x11.NewMockGetImageCookie(&xproto.GetImageReply{Depth: 24, Visual: 1, Data: data}, nil)
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
	b.conn.(*x11.MockConnection).GetImageFunc = func(format byte, drawable xproto.Drawable, x, y int16, width, height uint16, planeMask uint32) x11.GetImageCookie {
		return x11.NewMockGetImageCookie(&xproto.GetImageReply{Depth: 24, Visual: 1, Data: nil}, nil)
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
