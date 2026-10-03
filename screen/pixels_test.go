package screen

import (
	"image"
	"image/color"
	"testing"

	"github.com/nskaggs/perfuncted/find"
)

func TestPixelHashBGRA_MatchesDecodedFrames(t *testing.T) {
	const width, height, stride = 4, 3, 20
	fullFrame := make([]byte, stride*height)
	for i := range fullFrame {
		fullFrame[i] = byte(i*37 + 11)
	}
	rects := []struct {
		name string
		rect image.Rectangle
	}{
		{name: "full frame"},
		{name: "explicit full frame", rect: image.Rect(0, 0, width, height)},
		{name: "subregion", rect: image.Rect(1, 1, 3, 3)},
		{name: "clipped region", rect: image.Rect(-1, 1, 3, 8)},
		{name: "outside region", rect: image.Rect(8, 8, 9, 9)},
	}
	for _, rect := range rects {
		t.Run(rect.name, func(t *testing.T) {
			var decoded *image.RGBA
			if rect.rect.Empty() {
				decoded = decodeBGRA(fullFrame, width, height, stride)
			} else {
				decoded = decodeBGRARect(fullFrame, width, height, stride, rect.rect)
			}
			got := find.PixelHashBGRA(fullFrame, width, height, stride, rect.rect)
			want := find.PixelHash(decoded, nil)
			if got != want {
				t.Fatalf("PixelHashBGRA() = %08x, decoded hash = %08x", got, want)
			}
		})
	}
}

func TestPixelHashBGRA_InvalidFrameMetadata(t *testing.T) {
	tests := []struct {
		name                  string
		data                  []byte
		width, height, stride int
	}{
		{name: "empty data", data: nil, width: 1, height: 1, stride: 4},
		{name: "zero width", data: []byte{1, 2, 3, 4}, width: 0, height: 1, stride: 4},
		{name: "zero height", data: []byte{1, 2, 3, 4}, width: 1, height: 0, stride: 4},
		{name: "short stride", data: []byte{1, 2, 3, 4}, width: 2, height: 1, stride: 4},
		{name: "incomplete frame", data: []byte{1, 2, 3, 4}, width: 1, height: 2, stride: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := find.PixelHashBGRA(tt.data, tt.width, tt.height, tt.stride, image.Rectangle{}); got != 0 {
				t.Fatalf("PixelHashBGRA() = %08x, want zero hash for invalid frame metadata", got)
			}
		})
	}
}

func TestPixelHashBGRA_WideRowsMatchDecodedFrames(t *testing.T) {
	const width, height, stride = 9000, 2, 36016
	data := make([]byte, stride*height)
	for i := range data {
		data[i] = byte(i*19 + 7)
	}
	for _, rect := range []image.Rectangle{
		{},
		image.Rect(123, 0, 8900, height),
	} {
		var decoded *image.RGBA
		if rect.Empty() {
			decoded = decodeBGRA(data, width, height, stride)
		} else {
			decoded = decodeBGRARect(data, width, height, stride, rect)
		}
		got := find.PixelHashBGRA(data, width, height, stride, rect)
		if want := find.PixelHash(decoded, nil); got != want {
			t.Fatalf("PixelHashBGRA(%v) = %08x, decoded hash = %08x", rect, got, want)
		}
	}
}

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
	if got := color.RGBAModel.Convert(cropped.At(1, 1)).(color.RGBA); got != src.c { //nolint:errcheck // color.RGBAModel.Convert always returns color.RGBA
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

func TestCropRGBARejectsOverflowingRectangles(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	src := image.NewRGBA(image.Rect(0, 0, 1, 1))

	if cropped := cropRGBA(src, image.Rectangle{Max: image.Point{X: maxInt, Y: 2}}); !cropped.Bounds().Empty() {
		t.Fatalf("cropRGBA accepted overflowing width: %v", cropped.Bounds())
	}
	if cropped := cropRGBA(src, image.Rectangle{Min: image.Point{X: -maxInt}, Max: image.Point{X: maxInt, Y: 1}}); !cropped.Bounds().Empty() {
		t.Fatalf("cropRGBA accepted overflowing mixed-sign width: %v", cropped.Bounds())
	}
	if cropped := cropRGBA(nil, image.Rect(0, 0, 1, 1)); !cropped.Bounds().Empty() {
		t.Fatalf("cropRGBA returned non-empty image for nil source: %v", cropped.Bounds())
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

func TestDecodeBGRARejectsShortStride(t *testing.T) {
	data := []byte{
		0x01, 0x02, 0x03, 0xFF,
		0x04, 0x05, 0x06, 0xFF,
	}

	if img := decodeBGRA(data, 2, 1, 4); !img.Bounds().Empty() {
		t.Fatalf("decodeBGRA with short stride bounds = %v, want empty", img.Bounds())
	}
	if img := decodeBGRARect(data, 2, 1, 4, image.Rect(0, 0, 1, 1)); !img.Bounds().Empty() {
		t.Fatalf("decodeBGRARect with short stride bounds = %v, want empty", img.Bounds())
	}
}

func TestDecodeBGRARejectsOverflowingDimensions(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	if img := decodeBGRA([]byte{1}, maxInt, 1, maxInt); !img.Bounds().Empty() {
		t.Fatalf("decodeBGRA accepted overflowing dimensions: %v", img.Bounds())
	}
	if img := decodeBGRARect([]byte{1}, maxInt, 1, maxInt, image.Rect(0, 0, 1, 1)); !img.Bounds().Empty() {
		t.Fatalf("decodeBGRARect accepted overflowing dimensions: %v", img.Bounds())
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

func TestCropRGBAShortBufferDoesNotPanic(t *testing.T) {
	src := &image.RGBA{
		Pix:    make([]byte, 4),
		Stride: 16,
		Rect:   image.Rect(0, 0, 4, 4),
	}
	cropped := cropRGBA(src, image.Rect(0, 0, 4, 4))
	if cropped == nil {
		t.Fatal("expected non-nil cropped image")
	}
}

// A capture of a region that does not reach the output decodes to a zero-sized
// image. Returning that as a successful capture reads as a transparent black
// frame, so the seam that hands an image to a caller turns it into an error.
func TestRequireCapturedImageRejectsEmptyDecode(t *testing.T) {
	const width, height, stride = 4, 3, 20
	pixels := make([]byte, stride*height)
	for i := range pixels {
		pixels[i] = byte(i*37 + 11)
	}

	regions := []struct {
		name string
		rect image.Rectangle
	}{
		{name: "past the right edge", rect: image.Rect(8, 0, 10, 2)},
		{name: "below the bottom edge", rect: image.Rect(0, 9, 2, 10)},
		{name: "entirely past the output", rect: image.Rect(50, 50, 60, 60)},
		{name: "wholly negative", rect: image.Rect(-20, -20, -10, -10)},
	}
	for _, region := range regions {
		t.Run(region.name, func(t *testing.T) {
			decoded := decodeBGRARect(pixels, width, height, stride, region.rect)
			if !decoded.Bounds().Empty() {
				t.Fatalf("decodeBGRARect(%v) = %v, want an empty image", region.rect, decoded.Bounds())
			}
			if _, err := requireCapturedImage("test", decoded); err == nil {
				t.Fatalf("requireCapturedImage accepted the empty decode of %v", region.rect)
			}
		})
	}
}

// A decode of a region that does reach the output is passed through unchanged.
func TestRequireCapturedImagePassesThroughUsableDecode(t *testing.T) {
	const width, height, stride = 4, 3, 20
	pixels := make([]byte, stride*height)
	for i := range pixels {
		pixels[i] = byte(i*37 + 11)
	}

	decoded := decodeBGRARect(pixels, width, height, stride, image.Rect(1, 1, 3, 3))
	if decoded.Bounds().Empty() {
		t.Fatal("decodeBGRARect produced an empty image for an in-bounds region")
	}
	got, err := requireCapturedImage("test", decoded)
	if err != nil {
		t.Fatalf("requireCapturedImage: %v", err)
	}
	if got.Bounds() != decoded.Bounds() {
		t.Fatalf("bounds = %v, want %v", got.Bounds(), decoded.Bounds())
	}
}

// A backend that never decoded anything at all is an error too, rather than a
// nil image handed back with a nil error.
func TestRequireCapturedImageRejectsNil(t *testing.T) {
	if _, err := requireCapturedImage("test", nil); err == nil {
		t.Fatal("requireCapturedImage accepted a nil image")
	}
}
