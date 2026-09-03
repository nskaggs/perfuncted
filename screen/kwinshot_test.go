package screen

import (
	"context"
	"image"
	"image/color"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/find"
)

type fakeKWinTransport struct {
	active      image.Image
	area        image.Image
	activeErr   error
	areaErr     error
	activeCalls int
	areaCalls   int
	areaRects   []image.Rectangle
}

func (f *fakeKWinTransport) CaptureActiveScreen(context.Context) (image.Image, error) {
	f.activeCalls++
	if f.activeErr != nil {
		return nil, f.activeErr
	}
	return f.active, nil
}

func (f *fakeKWinTransport) CaptureArea(_ context.Context, rect image.Rectangle) (image.Image, error) {
	f.areaCalls++
	f.areaRects = append(f.areaRects, rect)
	if f.areaErr != nil {
		return nil, f.areaErr
	}
	return f.area, nil
}

func solidRGBAForKWin(c color.RGBA, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func TestKWinShotBackend_GrabUsesActiveScreenForEmptyRect(t *testing.T) {
	active := solidRGBAForKWin(color.RGBA{R: 10, G: 20, B: 30, A: 255}, 3, 2)
	area := solidRGBAForKWin(color.RGBA{R: 90, G: 80, B: 70, A: 255}, 1, 1)
	transport := &fakeKWinTransport{active: active, area: area}
	b := &KWinShotBackend{transport: transport}

	img, err := b.Grab(context.Background(), image.Rectangle{})
	if err != nil {
		t.Fatalf("Grab(empty): %v", err)
	}
	if transport.activeCalls != 1 || transport.areaCalls != 0 {
		t.Fatalf("calls = active:%d area:%d, want active:1 area:0", transport.activeCalls, transport.areaCalls)
	}
	if img.Bounds() != active.Bounds() {
		t.Fatalf("Grab(empty) bounds = %v, want %v", img.Bounds(), active.Bounds())
	}
	if got, want := find.PixelHash(img, nil), find.PixelHash(active, nil); got != want {
		t.Fatalf("Grab(empty) hash = %08x, want %08x", got, want)
	}
}

func TestKWinShotBackend_GrabUsesAreaForNonEmptyRect(t *testing.T) {
	active := solidRGBAForKWin(color.RGBA{R: 10, G: 20, B: 30, A: 255}, 3, 2)
	area := solidRGBAForKWin(color.RGBA{R: 90, G: 80, B: 70, A: 255}, 2, 2)
	transport := &fakeKWinTransport{active: active, area: area}
	b := &KWinShotBackend{transport: transport}
	rect := image.Rect(4, 5, 6, 7)

	img, err := b.Grab(context.Background(), rect)
	if err != nil {
		t.Fatalf("Grab(rect): %v", err)
	}
	if transport.activeCalls != 0 || transport.areaCalls != 1 {
		t.Fatalf("calls = active:%d area:%d, want active:0 area:1", transport.activeCalls, transport.areaCalls)
	}
	if len(transport.areaRects) != 1 || transport.areaRects[0] != rect {
		t.Fatalf("area rects = %v, want %v", transport.areaRects, rect)
	}
	if got, want := img.Bounds(), area.Bounds(); got != want {
		t.Fatalf("Grab(rect) bounds = %v, want %v", got, want)
	}
}

func TestValidateKWinRect(t *testing.T) {
	tests := []struct {
		name string
		rect image.Rectangle
		want bool
	}{
		{name: "valid negative origin", rect: image.Rect(-100, -50, 900, 650), want: true},
		{name: "x origin overflows int32", rect: image.Rect(1<<31, 0, 1<<31+1, 1), want: false},
		{name: "y origin underflows int32", rect: image.Rect(0, -1<<31-1, 1, -1<<31), want: false},
		{name: "width overflows uint32", rect: image.Rectangle{Min: image.Point{X: 0, Y: 0}, Max: image.Point{X: 1 << 32, Y: 1}}, want: false},
		{name: "height overflows uint32", rect: image.Rectangle{Min: image.Point{X: 0, Y: 0}, Max: image.Point{X: 1, Y: 1 << 32}}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateKWinRect(tt.rect)
			if (err == nil) != tt.want {
				t.Fatalf("validateKWinRect(%v) error = %v, want valid=%v", tt.rect, err, tt.want)
			}
		})
	}
}

func TestKWinShotBackend_GrabFullHashUsesActiveScreen(t *testing.T) {
	active := solidRGBAForKWin(color.RGBA{R: 1, G: 2, B: 3, A: 255}, 2, 2)
	transport := &fakeKWinTransport{active: active}
	b := &KWinShotBackend{transport: transport}

	got, err := b.GrabFullHash(context.Background())
	if err != nil {
		t.Fatalf("GrabFullHash: %v", err)
	}
	if transport.activeCalls != 1 || transport.areaCalls != 0 {
		t.Fatalf("calls = active:%d area:%d, want active:1 area:0", transport.activeCalls, transport.areaCalls)
	}
	if want := find.PixelHash(active, nil); got != want {
		t.Fatalf("GrabFullHash = %08x, want %08x", got, want)
	}
}

func TestKWinShotBackend_ResolutionUsesActiveScreen(t *testing.T) {
	active := solidRGBAForKWin(color.RGBA{R: 4, G: 5, B: 6, A: 255}, 5, 4)
	transport := &fakeKWinTransport{active: active}
	b := &KWinShotBackend{transport: transport}

	w, h, err := b.Resolution()
	if err != nil {
		t.Fatalf("Resolution: %v", err)
	}
	if w != 5 || h != 4 {
		t.Fatalf("Resolution = %dx%d, want 5x4", w, h)
	}
	if transport.activeCalls != 1 || transport.areaCalls != 0 {
		t.Fatalf("calls = active:%d area:%d, want active:1 area:0", transport.activeCalls, transport.areaCalls)
	}
}

func TestDecodeKWinPixels_UsesResultDimensions(t *testing.T) {
	// Raw ARGB32/BGRA bytes for two opaque pixels.
	data := []byte{
		3, 2, 1, 255,
		6, 5, 4, 255,
	}
	results := map[string]dbus.Variant{
		"width":  dbus.MakeVariant(uint32(2)),
		"height": dbus.MakeVariant(uint32(1)),
		"stride": dbus.MakeVariant(uint32(8)),
		"format": dbus.MakeVariant(uint32(4)),
	}

	img, err := decodeKWinPixels(data, image.Rectangle{}, results)
	if err != nil {
		t.Fatalf("decodeKWinPixels: %v", err)
	}
	if got, want := img.Bounds(), image.Rect(0, 0, 2, 1); got != want {
		t.Fatalf("decodeKWinPixels bounds = %v, want %v", got, want)
	}

	expected := image.NewRGBA(image.Rect(0, 0, 2, 1))
	expected.SetRGBA(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	expected.SetRGBA(1, 0, color.RGBA{R: 4, G: 5, B: 6, A: 255})
	if got, want := find.PixelHash(img, nil), find.PixelHash(expected, nil); got != want {
		t.Fatalf("decodeKWinPixels hash = %08x, want %08x", got, want)
	}
}

func TestDecodeKWinPixelsRejectsInvalidGeometry(t *testing.T) {
	tests := []struct {
		name    string
		results map[string]dbus.Variant
	}{
		{
			name: "short stride",
			results: map[string]dbus.Variant{
				"width":  dbus.MakeVariant(uint32(2)),
				"height": dbus.MakeVariant(uint32(1)),
				"stride": dbus.MakeVariant(uint32(4)),
			},
		},
		{
			name: "zero dimensions",
			results: map[string]dbus.Variant{
				"width":  dbus.MakeVariant(uint32(0)),
				"height": dbus.MakeVariant(uint32(1)),
				"stride": dbus.MakeVariant(uint32(4)),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := decodeKWinPixels(make([]byte, 8), image.Rectangle{}, tt.results); err == nil {
				t.Fatal("decodeKWinPixels unexpectedly accepted invalid geometry")
			}
		})
	}
}
