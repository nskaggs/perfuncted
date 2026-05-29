package screen

import (
	"image"
	"image/color"
	"testing"
)

type solidTestImage struct {
	rect image.Rectangle
	c    color.RGBA
}

func (s solidTestImage) ColorModel() color.Model { return color.RGBAModel }
func (s solidTestImage) Bounds() image.Rectangle { return s.rect }
func (s solidTestImage) At(x, y int) color.Color {
	if !image.Pt(x, y).In(s.rect) {
		return color.RGBA{}
	}
	return s.c
}

func TestDecodeBGRA(t *testing.T) {
	// 2x2 image, tightly packed (stride=8).
	data := []byte{
		// row 0
		0x10, 0x20, 0x30, 0xFF, // pixel (0,0): B=0x10, G=0x20, R=0x30
		0x40, 0x50, 0x60, 0xFF, // pixel (1,0): B=0x40, G=0x50, R=0x60
		// row 1
		0xA0, 0xB0, 0xC0, 0xFF, // pixel (0,1): B=0xA0, G=0xB0, R=0xC0
		0x00, 0x00, 0x00, 0xFF, // pixel (1,1): black
	}
	img := decodeBGRA(data, 2, 2, 8)

	tests := []struct {
		x, y    int
		r, g, b uint8
	}{
		{0, 0, 0x30, 0x20, 0x10},
		{1, 0, 0x60, 0x50, 0x40},
		{0, 1, 0xC0, 0xB0, 0xA0},
		{1, 1, 0x00, 0x00, 0x00},
	}
	for _, tc := range tests {
		c := img.RGBAAt(tc.x, tc.y)
		if c.R != tc.r || c.G != tc.g || c.B != tc.b {
			t.Errorf("(%d,%d): got RGB(%02x,%02x,%02x) want (%02x,%02x,%02x)",
				tc.x, tc.y, c.R, c.G, c.B, tc.r, tc.g, tc.b)
		}
		if c.A != 0xFF {
			t.Errorf("(%d,%d): alpha=%d want 255", tc.x, tc.y, c.A)
		}
	}
}

func TestDecodeBGRAWithStridePadding(t *testing.T) {
	// 1x2 image with stride=8 (4 bytes padding per row).
	data := []byte{
		0x01, 0x02, 0x03, 0xFF, 0x00, 0x00, 0x00, 0x00, // row 0 + padding
		0x04, 0x05, 0x06, 0xFF, 0x00, 0x00, 0x00, 0x00, // row 1 + padding
	}
	img := decodeBGRA(data, 1, 2, 8)

	c0 := img.RGBAAt(0, 0)
	if c0.R != 0x03 || c0.G != 0x02 || c0.B != 0x01 {
		t.Errorf("row 0: got RGB(%02x,%02x,%02x) want (03,02,01)", c0.R, c0.G, c0.B)
	}
	c1 := img.RGBAAt(0, 1)
	if c1.R != 0x06 || c1.G != 0x05 || c1.B != 0x04 {
		t.Errorf("row 1: got RGB(%02x,%02x,%02x) want (06,05,04)", c1.R, c1.G, c1.B)
	}
}

func TestCropRGBA(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			src.SetRGBA(x, y, color.RGBA{R: uint8(x * 25), G: uint8(y * 25), B: 128, A: 255})
		}
	}

	cropped := cropRGBA(src, image.Rect(2, 3, 5, 6))
	if cropped.Bounds().Dx() != 3 || cropped.Bounds().Dy() != 3 {
		t.Fatalf("expected 3x3, got %dx%d", cropped.Bounds().Dx(), cropped.Bounds().Dy())
	}
	// Check that pixel (0,0) of cropped = pixel (2,3) of src.
	expected := src.RGBAAt(2, 3)
	got := cropped.RGBAAt(0, 0)
	if got != expected {
		t.Errorf("(0,0): got %v want %v", got, expected)
	}
	// Check corner (2,2) of cropped = pixel (4,5) of src.
	expected = src.RGBAAt(4, 5)
	got = cropped.RGBAAt(2, 2)
	if got != expected {
		t.Errorf("(2,2): got %v want %v", got, expected)
	}
}

func TestCropImageCopiesNonSubImage(t *testing.T) {
	src := solidTestImage{rect: image.Rect(0, 0, 4, 4), c: color.RGBA{R: 7, G: 8, B: 9, A: 255}}
	cropped := cropImage(src, image.Rect(1, 1, 3, 3))
	if got, want := cropped.Bounds(), image.Rect(1, 1, 3, 3); got != want {
		t.Fatalf("cropImage bounds = %v, want %v", got, want)
	}
	if got := color.RGBAModel.Convert(cropped.At(1, 1)).(color.RGBA); got != src.c {
		t.Fatalf("cropImage pixel = %#v, want %#v", got, src.c)
	}
}

func TestCropRGBAClipsToSource(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 5, 5))
	src.SetRGBA(4, 4, color.RGBA{R: 99, G: 99, B: 99, A: 255})

	cropped := cropRGBA(src, image.Rect(3, 3, 10, 10))
	if cropped.Bounds().Dx() != 7 || cropped.Bounds().Dy() != 7 {
		t.Fatalf("cropped bounds should be 7x7, got %dx%d", cropped.Bounds().Dx(), cropped.Bounds().Dy())
	}
	// (1,1) in cropped = (4,4) in src.
	c := cropped.RGBAAt(1, 1)
	if c.R != 99 {
		t.Errorf("expected R=99, got %d", c.R)
	}
	// (5,5) in cropped = (8,8) in src -> out of bounds, should be zero.
	c = cropped.RGBAAt(5, 5)
	if c.R != 0 || c.G != 0 || c.B != 0 || c.A != 0 {
		t.Errorf("out-of-bounds pixel should be zero, got %v", c)
	}
}

func TestDecodeBGRARect(t *testing.T) {
	// 3x3 image, stride=12.
	data := make([]byte, 36)
	// Fill with distinct values
	for i := 0; i < 9; i++ {
		data[i*4+0] = byte(i + 1)       // B
		data[i*4+1] = byte((i + 1) * 2) // G
		data[i*4+2] = byte((i + 1) * 3) // R
		data[i*4+3] = 0xFF              // A
	}

	// Extract 2x2 rect starting at (1, 1)
	rect := image.Rect(1, 1, 3, 3)
	img := decodeBGRARect(data, 3, 3, 12, rect)

	if got := img.Bounds(); got != rect {
		t.Fatalf("expected bounds %v, got %v", rect, got)
	}

	// Check (1, 1) of cropped/original = index 4.
	c0 := img.RGBAAt(1, 1)
	expectedB := byte(5)
	expectedG := byte(10)
	expectedR := byte(15)

	if c0.B != expectedB || c0.G != expectedG || c0.R != expectedR {
		t.Errorf("expected RGB(%d, %d, %d), got (%d, %d, %d)", expectedR, expectedG, expectedB, c0.R, c0.G, c0.B)
	}
}

func TestDecodeBGRARectWithStridePadding(t *testing.T) {
	// 2x2 image with stride=12 (4 bytes padding per row).
	data := []byte{
		0x01, 0x02, 0x03, 0xFF, 0x11, 0x12, 0x13, 0xFF, 0x00, 0x00, 0x00, 0x00, // row 0 + padding
		0x04, 0x05, 0x06, 0xFF, 0x14, 0x15, 0x16, 0xFF, 0x00, 0x00, 0x00, 0x00, // row 1 + padding
	}

	// Extract 1x2 rect starting at (1, 0)
	rect := image.Rect(1, 0, 2, 2)
	img := decodeBGRARect(data, 2, 2, 12, rect)

	if got := img.Bounds(); got != rect {
		t.Fatalf("expected bounds %v, got %v", rect, got)
	}

	c0 := img.RGBAAt(1, 0)
	if c0.B != 0x11 || c0.G != 0x12 || c0.R != 0x13 {
		t.Errorf("row 0: got RGB(%02x,%02x,%02x) want (13,12,11)", c0.R, c0.G, c0.B)
	}

	c1 := img.RGBAAt(1, 1)
	if c1.B != 0x14 || c1.G != 0x15 || c1.R != 0x16 {
		t.Errorf("row 1: got RGB(%02x,%02x,%02x) want (16,15,14)", c1.R, c1.G, c1.B)
	}
}

func TestDecodeBGRAShortDataDoesNotPanic(t *testing.T) {
	// 2x2 image, but only the first row is present.
	data := []byte{
		0x01, 0x02, 0x03, 0xFF,
		0x04, 0x05, 0x06, 0xFF,
	}

	img := decodeBGRA(data, 2, 2, 8)
	if got := img.Bounds(); got != image.Rect(0, 0, 2, 2) {
		t.Fatalf("bounds = %v, want 2x2", got)
	}
	if c := img.RGBAAt(0, 0); c.R != 0x03 || c.G != 0x02 || c.B != 0x01 || c.A != 0xFF {
		t.Fatalf("top-left pixel = %+v, want RGBA(03,02,01,FF)", c)
	}
	if c := img.RGBAAt(1, 0); c.R != 0x06 || c.G != 0x05 || c.B != 0x04 || c.A != 0xFF {
		t.Fatalf("top-right pixel = %+v, want RGBA(06,05,04,FF)", c)
	}
	if c := img.RGBAAt(0, 1); c != (color.RGBA{}) {
		t.Fatalf("bottom-left pixel = %+v, want zero value", c)
	}
}

func TestDecodeBGRARectShortDataDoesNotPanic(t *testing.T) {
	// 2x2 image, but only the first row is present.
	data := []byte{
		0x01, 0x02, 0x03, 0xFF,
		0x04, 0x05, 0x06, 0xFF,
	}

	rect := image.Rect(0, 0, 2, 2)
	img := decodeBGRARect(data, 2, 2, 8, rect)
	if got := img.Bounds(); got != rect {
		t.Fatalf("bounds = %v, want %v", got, rect)
	}
	if c := img.RGBAAt(0, 0); c.R != 0x03 || c.G != 0x02 || c.B != 0x01 || c.A != 0xFF {
		t.Fatalf("top-left pixel = %+v, want RGBA(03,02,01,FF)", c)
	}
	if c := img.RGBAAt(1, 0); c.R != 0x06 || c.G != 0x05 || c.B != 0x04 || c.A != 0xFF {
		t.Fatalf("top-right pixel = %+v, want RGBA(06,05,04,FF)", c)
	}
	if c := img.RGBAAt(0, 1); c != (color.RGBA{}) {
		t.Fatalf("bottom-left pixel = %+v, want zero value", c)
	}
}
