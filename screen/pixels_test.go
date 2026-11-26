package screen

import (
	"bytes"
	"errors"
	"github.com/nskaggs/perfuncted/v2/find"
	"image"
	"image/color"
	"math"
	"math/rand"
	"slices"
	"testing"
)

// A region read and the same region of a full compositor frame must produce
// identical pixels, including with row padding and clipped regions. This
// protects screenshot backends that use the fast region-only decode path.
func TestBGRARegionDecodeAgreesWithFullFrameAcrossPaddedLayouts(t *testing.T) {
	rng := rand.New(rand.NewSource(34918))
	for iteration := 0; iteration < 150; iteration++ {
		width := 1 + rng.Intn(18)
		height := 1 + rng.Intn(14)
		stride := 4*width + 4*rng.Intn(5)
		frame := make([]byte, height*stride)
		if _, err := rng.Read(frame); err != nil {
			t.Fatal(err)
		}
		full := decodeBGRA(frame, width, height, stride)
		rect := image.Rect(rng.Intn(width+9)-4, rng.Intn(height+9)-4, rng.Intn(width+9)-4, rng.Intn(height+9)-4)
		// Include arbitrary corners rather than requiring the requested region
		// to fall entirely within the compositor's output.
		expectedRect := rect.Intersect(full.Bounds())
		if expectedRect.Empty() {
			expectedRect = image.Rectangle{}
		}
		region := decodeBGRARect(frame, width, height, stride, rect)
		if region.Bounds() != expectedRect {
			t.Fatalf("case %d: region bounds %v, want %v", iteration, region.Bounds(), expectedRect)
		}
		for y := expectedRect.Min.Y; y < expectedRect.Max.Y; y++ {
			for x := expectedRect.Min.X; x < expectedRect.Max.X; x++ {
				if got, want := region.RGBAAt(x, y), full.RGBAAt(x, y); got != want {
					t.Fatalf("case %d at (%d,%d): region=%v full=%v", iteration, x, y, got, want)
				}
				if region.RGBAAt(x, y).A != 255 {
					t.Fatalf("case %d: decoder did not canonicalize alpha", iteration)
				}
			}
		}
	}
}

func TestBGRAIncompleteTransportNeverReusesPreviousPixelData(t *testing.T) {
	// The compositor can terminate mid-frame. Decoding must only expose bytes
	// present in the current capture and must not leak bytes from prior frames.
	const width, height, stride = 3, 2, 16
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 0, 0, 0, 0, 13, 14, 15, 16}
	for _, tc := range []struct {
		name       string
		decode     func() *image.RGBA
		bounds     image.Rectangle
		filled     image.Point
		incomplete image.Point
	}{
		{"full padded frame", func() *image.RGBA { return decodeBGRA(data, width, height, stride) }, image.Rect(0, 0, 3, 2), image.Pt(0, 1), image.Pt(1, 1)},
		{"cropped padded frame", func() *image.RGBA { return decodeBGRARect(data, width, height, stride, image.Rect(0, 1, 3, 2)) }, image.Rect(0, 1, 3, 2), image.Pt(0, 1), image.Pt(1, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img := tc.decode()
			if img.Bounds() != tc.bounds {
				t.Fatalf("unexpected image extent %v want %v", img.Bounds(), tc.bounds)
			}
			got := img.RGBAAt(tc.filled.X, tc.filled.Y)
			if got != (color.RGBA{R: 15, G: 14, B: 13, A: 255}) {
				t.Fatalf("available pixel: %v", got)
			}
			if got := img.RGBAAt(tc.incomplete.X, tc.incomplete.Y); got != (color.RGBA{}) {
				t.Fatalf("incomplete pixel contains stale content: %v", got)
			}
		})
	}
}

func TestCropRGBADoesNotReadMalformedBackingStorage(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  *image.RGBA
	}{
		{"missing backing pixels", &image.RGBA{Pix: nil, Stride: 8, Rect: image.Rect(0, 0, 2, 2)}},
		{"truncated backing pixels", &image.RGBA{Pix: []byte{1, 2, 3, 4}, Stride: 8, Rect: image.Rect(0, 0, 2, 2)}},
		{"zero stride", &image.RGBA{Pix: make([]byte, 16), Stride: 0, Rect: image.Rect(0, 0, 2, 2)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			crop := cropRGBA(tc.src, image.Rect(0, 0, 2, 2))
			if !crop.Bounds().Empty() {
				t.Fatalf("malformed RGBA storage was returned as an image: %v", crop.Bounds())
			}
		})
	}
}

// ── Edge-case coverage ────────────────────────────────────────────────────────

// TestDecodeBGRARectEmptyRect verifies the early-return branch when the
// requested rectangle does not intersect the image bounds.
func TestDecodeBGRARectEmptyRect(t *testing.T) {
	data := make([]byte, 4*4*4) // 4×4 image, stride=16
	// Request a rect entirely outside the 4×4 frame.
	out := decodeBGRARect(data, 4, 4, 16, image.Rect(10, 10, 20, 20))
	if !out.Bounds().Empty() {
		t.Fatalf("expected empty output for out-of-bounds rect, got %v", out.Bounds())
	}
}

// TestCropRGBA_IdentityReturn verifies the early-return branch where the crop
// rectangle exactly matches the source image bounds starting at the origin.
func TestCropRGBA_IdentityReturn(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 5, 5))
	src.SetRGBA(2, 3, color.RGBA{R: 42, G: 84, B: 126, A: 255})

	result := cropRGBA(src, image.Rect(0, 0, 5, 5))
	if result != src {
		t.Fatal("cropRGBA should return src unchanged when rect equals full bounds at origin")
	}
}

// ── Benchmarks ────────────────────────────────────────────────────────────────

func BenchmarkDecodeBGRA(b *testing.B) {
	const w, h = 1920, 1080
	data := make([]byte, w*h*4)
	// Fill with non-trivial pattern.
	for i := range data {
		data[i] = byte(i)
	}

	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = decodeBGRA(data, w, h, w*4)
	}
}

func BenchmarkDecodeBGRARect(b *testing.B) {
	const w, h = 1920, 1080
	data := make([]byte, w*h*4)
	for i := range data {
		data[i] = byte(i)
	}
	// Crop a central 400×300 region.
	rect := image.Rect(760, 390, 1160, 690)

	b.SetBytes(int64(rect.Dx() * rect.Dy() * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = decodeBGRARect(data, w, h, w*4, rect)
	}
}

func BenchmarkCropRGBA(b *testing.B) {
	const w, h = 1920, 1080
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range src.Pix {
		src.Pix[i] = byte(i)
	}
	rect := image.Rect(100, 100, 500, 400)

	b.SetBytes(int64(rect.Dx() * rect.Dy() * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = cropRGBA(src, rect)
	}
}

// PNG-backed NRGBA and generic images must preserve crop size and transparent
// padding even when the requested rectangle is completely off-screen.
func TestCropImageDisjointNonRGBAPreservesRequestedGeometry(t *testing.T) {
	for _, wanted := range []image.Rectangle{image.Rect(8, 7, 12, 10), image.Rect(-8, -7, -4, -3)} {
		t.Run(wanted.String(), func(t *testing.T) {
			bounds := image.Rect(0, 0, 3, 3)
			rgba := image.NewRGBA(bounds)
			nrgba := image.NewNRGBA(bounds)
			rgba.SetRGBA(0, 0, color.RGBA{R: 90, A: 255})
			nrgba.SetNRGBA(0, 0, color.NRGBA{R: 90, A: 255})
			reference := cropImage(rgba, wanted)
			actual := cropImage(nrgba, wanted)
			if actual.Bounds() != reference.Bounds() {
				t.Fatalf("disjoint crop size mismatch: RGBA=%v NRGBA=%v", reference.Bounds(), actual.Bounds())
			}
			if r, g, b, a := actual.At(0, 0).RGBA(); r != 0 || g != 0 || b != 0 || a != 0 {
				t.Fatalf("disjoint crop pixel has nontransparent channels: %d %d %d %d", r, g, b, a)
			}
		})
	}
}

// External screenshot providers can return an ordinary NRGBA image while a
// caller supplies an enormous crop. Reject dimensions whose row or total
// byte counts overflow before allocating an output buffer.
func TestGenericImageCropRejectsUnallocatableRequestedDimensions(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	source := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for _, tc := range []struct {
		name string
		rect image.Rectangle
	}{
		{"unallocatable width", image.Rect(0, 0, maxInt, 1)},
		{"unallocatable height", image.Rect(0, 0, 1, maxInt)},
		{"unallocatable area", image.Rect(0, 0, maxInt/8, maxInt/8)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cropped := cropImage(source, tc.rect)
			if cropped == nil || !cropped.Bounds().Empty() {
				t.Fatalf("untrusted huge crop allocated image: %+v", cropped)
			}
		})
	}
	if got := cropImage(source, image.Rectangle{}); got != source {
		t.Fatal("empty region changed source image identity")
	}
}

// Dimension validation must fail closed independently for the column count,
// row count and multiplication, rather than overflow and appear plausible.
func TestPixelRectDimensionValidationRejectsOverflowAndReversedBoundaries(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	if w, h, ok := safePixelRectDimensions(image.Rectangle{}); !ok || w != 0 || h != 0 {
		t.Fatalf("empty rectangle invalid: %d %d %v", w, h, ok)
	}
	if _, _, ok := safePixelRectDimensions(image.Rect(0, 0, maxInt, 1)); ok {
		t.Fatal("overflowed row width accepted")
	}
	if _, _, ok := safePixelRectDimensions(image.Rect(0, 0, 1, maxInt)); ok {
		t.Fatal("overflowed total image allocation accepted")
	}
	if _, ok := safePixelDimension(2, 1); ok {
		t.Fatal("reversed span accepted")
	}
}

// Portal PNG decoders can produce NRGBA rather than RGBA. Cropping the same
// requested region must preserve its size and pixel mapping in either case,
// even when the requested region partly extends beyond the captured desktop.
func TestCropImageNonRGBAClippedRegionMatchesRGBACoordinates(t *testing.T) {
	bounds := image.Rect(0, 0, 3, 3)
	rgba := image.NewRGBA(bounds)
	nrgba := image.NewNRGBA(bounds)
	pixel := color.RGBA{R: 178, G: 45, B: 21, A: 255}
	rgba.SetRGBA(0, 0, pixel)
	nrgba.SetNRGBA(0, 0, color.NRGBA(pixel))
	wanted := image.Rect(-1, -1, 2, 2)
	reference := cropImage(rgba, wanted)
	actual := cropImage(nrgba, wanted)
	if actual.Bounds().Dx() != reference.Bounds().Dx() || actual.Bounds().Dy() != reference.Bounds().Dy() {
		t.Fatalf("clipped PNG variant changed crop dimensions: RGBA=%v NRGBA=%v", reference.Bounds(), actual.Bounds())
	}
	if got := color.RGBAModel.Convert(actual.At(actual.Bounds().Min.X+1, actual.Bounds().Min.Y+1)); got != pixel {
		t.Fatalf("clipped NRGBA pixel mapping=%v want %v", got, pixel)
	}
}

// Frame dimensions and crop coordinates come from external compositors.
// Bad metadata must not cause unsafe allocation or turn incomplete data into
// apparently valid nonzero pixels.
func TestRawBGRAFrameRejectsOverflowingHeightBeforeAllocation(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	if got := decodeBGRA([]byte{9}, 2, maxInt, 8); !got.Bounds().Empty() {
		t.Fatalf("overflow height accepted: %v", got.Bounds())
	}
}

func TestRawBGRARectRefusesInvalidAndUnreachableRegions(t *testing.T) {
	for _, tc := range []struct {
		name         string
		w, h, stride int
		rect         image.Rectangle
	}{
		{"empty bytes", 1, 1, 4, image.Rect(0, 0, 1, 1)},
		{"invalid height", 1, 0, 4, image.Rect(0, 0, 1, 1)},
		{"invalid stride", 1, 1, 0, image.Rect(0, 0, 1, 1)},
		{"outside output", 1, 1, 4, image.Rect(5, 5, 6, 6)},
		{"oversized width", int(^uint(0) >> 1), 1, 4, image.Rect(0, 0, 1, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte{1, 2, 3, 4}
			if tc.name == "empty bytes" {
				data = nil
			}
			if img := decodeBGRARect(data, tc.w, tc.h, tc.stride, tc.rect); !img.Bounds().Empty() {
				t.Fatalf("invalid frame produced pixels: %v", img.Bounds())
			}
		})
	}
}

func TestCropRGBARejectsMalformedStorageAndPreservesTransparentGaps(t *testing.T) {
	source := &image.RGBA{Pix: []byte{1, 2, 3, 4}, Stride: 0, Rect: image.Rect(0, 0, 2, 2)}
	cropped := cropRGBA(source, image.Rect(0, 0, 1, 1))
	if got := cropped.RGBAAt(0, 0); got != (color.RGBA{}) {
		t.Fatalf("zero-stride frame disclosed pixels: %+v", got)
	}
	source.Stride = 8
	source.Rect = image.Rect(0, 0, 2, 2)
	cropped = cropRGBA(source, image.Rect(-1, -1, 3, 3))
	if cropped.Bounds() != image.Rect(0, 0, 4, 4) {
		t.Fatalf("crop should preserve requested geometry: %v", cropped.Bounds())
	}
	for _, p := range []image.Point{{0, 0}, {1, 1}, {2, 2}, {3, 3}} {
		if got := cropped.RGBAAt(p.X, p.Y); got != (color.RGBA{}) {
			t.Fatalf("invalid backing pixels became visible at %v: %v", p, got)
		}
	}
}

func TestCropRGBARejectsMalformedSourceRectangleDimensions(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	source := &image.RGBA{Pix: []byte{1, 2, 3, 4}, Stride: 4, Rect: image.Rectangle{Min: image.Pt(-maxInt, 0), Max: image.Pt(maxInt, 1)}}
	got := cropRGBA(source, image.Rect(0, 0, 1, 1))
	if c := got.RGBAAt(0, 0); c != (color.RGBA{}) {
		t.Fatalf("overflowing source exposed pixels: %v", c)
	}
}

func TestCropImageOutsideRegionNeverReadsUnderlyingImage(t *testing.T) {
	source := solidTestImage{rect: image.Rect(0, 0, 1, 1), c: color.RGBA{R: 7, A: 255}}
	got := cropImage(source, image.Rect(10, 10, 12, 12))
	if !got.Bounds().Empty() {
		t.Fatalf("out-of-range non-subimage crop reported data: %v", got.Bounds())
	}
}

// The requested area can have valid int coordinates yet exceed the number of
// addressable pixels. Reject it before allocating or indexing untrusted frame
// bytes, without requiring the test to allocate a large image.
func TestDecodeBGRARectRejectsOversizedPixelAreaBeforeAllocating(t *testing.T) {
	img := decodeBGRARect([]byte{0, 0, 0, 0}, 2, math.MaxInt, 8, image.Rect(0, 0, 2, math.MaxInt))
	if img == nil || !img.Bounds().Empty() || len(img.Pix) != 0 {
		t.Fatalf("oversized compositor rectangle accepted: bounds=%v bytes=%d", img.Bounds(), len(img.Pix))
	}
}

// Compositor pixel buffers are untrusted. A row with an impossible source
// address must not copy any bytes into the destination, even if both backing
// slices happen to contain data.
func TestRGBARowCopyRejectsInvalidSourceAddressWithoutChangingDestination(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	tests := []struct {
		name   string
		src    *image.RGBA
		region image.Rectangle
		y      int
	}{
		{"negative source row", &image.RGBA{Pix: make([]byte, 16), Stride: 8, Rect: image.Rect(0, 3, 2, 5)}, image.Rect(0, 0, 2, 1), 0},
		{"negative source column", &image.RGBA{Pix: make([]byte, 16), Stride: 8, Rect: image.Rect(3, 0, 5, 2)}, image.Rect(0, 0, 2, 1), 0},
		{"source row offset overflow", &image.RGBA{Pix: make([]byte, 16), Stride: 8, Rect: image.Rect(0, 0, 2, 2)}, image.Rect(0, 0, 2, 1), maxInt},
		{"source column byte offset overflow", &image.RGBA{Pix: make([]byte, 16), Stride: 8, Rect: image.Rect(0, 0, 2, 2)}, image.Rect(maxInt/4+1, 0, maxInt/4+3, 1), 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dst := image.NewRGBA(image.Rect(0, 0, 2, 2))
			dst.SetRGBA(0, 0, color.RGBA{R: 33, G: 44, B: 55, A: 255})
			before := append([]byte(nil), dst.Pix...)
			copyRGBARow(dst, tc.src, tc.region, 0, 0, tc.y, 8)
			for i := range before {
				if before[i] != dst.Pix[i] {
					t.Fatalf("invalid source addressed destination at offset %d", i)
				}
			}
		})
	}
}

func TestPixelRectDimensionsRejectsCoordinateSubtractionOverflow(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	if _, _, ok := safePixelRectDimensions(image.Rectangle{Min: image.Pt(0, -maxInt), Max: image.Pt(1, maxInt)}); ok {
		t.Fatal("overflowing row coordinate span accepted")
	}
}

// A decoder must never partially copy a compositor row whose source or
// destination byte storage cannot hold the complete requested pixel span.
func TestRGBARowCopyRejectsTruncatedCompositorStorageWithoutPartialWrite(t *testing.T) {
	valid := image.NewRGBA(image.Rect(0, 0, 2, 1))
	for i := range valid.Pix {
		valid.Pix[i] = byte(i + 1)
	}
	tests := []struct {
		name        string
		destination *image.RGBA
		source      *image.RGBA
	}{
		{name: "source truncated", destination: image.NewRGBA(image.Rect(0, 0, 2, 1)), source: &image.RGBA{Pix: []byte{1, 2, 3, 4}, Stride: 8, Rect: image.Rect(0, 0, 2, 1)}},
		{name: "destination truncated", destination: &image.RGBA{Pix: []byte{9, 9, 9, 9}, Stride: 8, Rect: image.Rect(0, 0, 2, 1)}, source: valid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for i := range tc.destination.Pix {
				tc.destination.Pix[i] = 0xaa
			}
			before := bytes.Clone(tc.destination.Pix)
			copyRGBARow(tc.destination, tc.source, image.Rect(0, 0, 2, 1), 0, 0, 0, 8)
			if !bytes.Equal(tc.destination.Pix, before) {
				t.Fatalf("partial pixel bytes escaped truncated storage: got=%x want=%x", tc.destination.Pix, before)
			}
		})
	}
}

// A compositor may report a row stride close to MaxInt while providing only
// the first row of a frame. Decoding later rows must not overflow row offsets
// or turn absent pixels into fabricated opaque content.
func TestBGRADecodersRejectOverflowingLaterRowOffsets(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	source := []byte{9, 19, 29, 39}
	full := decodeBGRA(source, 1, 3, maxInt)
	if full.Bounds() != image.Rect(0, 0, 1, 3) {
		t.Fatalf("full frame bounds = %v", full.Bounds())
	}
	if got := full.RGBAAt(0, 0); got != (color.RGBA{R: 29, G: 19, B: 9, A: 255}) {
		t.Fatalf("available first row decoded incorrectly: %+v", got)
	}
	for y := 1; y < 3; y++ {
		if got := full.RGBAAt(0, y); got != (color.RGBA{}) {
			t.Fatalf("unavailable row %d fabricated pixels: %+v", y, got)
		}
	}

	// The second row's offset is (MaxInt + 4), which cannot be expressed in
	// an int. The rect decoder must stop before multiplying and adding it.
	region := decodeBGRARect(source, 2, 2, maxInt, image.Rect(1, 1, 2, 2))
	if region.Bounds() != image.Rect(1, 1, 2, 2) {
		t.Fatalf("subregion bounds = %v", region.Bounds())
	}
	if got := region.RGBAAt(1, 1); got != (color.RGBA{}) {
		t.Fatalf("overflowing source row yielded pixels: %+v", got)
	}
}

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
	}
	for _, rect := range rects {
		t.Run(rect.name, func(t *testing.T) {
			var decoded *image.RGBA
			if rect.rect.Empty() {
				decoded = decodeBGRA(fullFrame, width, height, stride)
			} else {
				decoded = decodeBGRARect(fullFrame, width, height, stride, rect.rect)
			}
			got, hashErr := find.PixelHashBGRA(fullFrame, width, height, stride, rect.rect)
			if hashErr != nil {
				t.Fatalf("PixelHashBGRA() error = %v", hashErr)
			}
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
			got, err := find.PixelHashBGRA(tt.data, tt.width, tt.height, tt.stride, image.Rectangle{})
			if !errors.Is(err, find.ErrInvalidFrame) {
				t.Fatalf("PixelHashBGRA() error = %v, want ErrInvalidFrame for invalid frame metadata", err)
			}
			if got != 0 {
				t.Fatalf("PixelHashBGRA() = %08x alongside an error, want no checksum", got)
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
		got, hashErr := find.PixelHashBGRA(data, width, height, stride, rect)
		if hashErr != nil {
			t.Fatalf("PixelHashBGRA() error = %v", hashErr)
		}
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

// A region entirely outside the frame has no pixels to hash. Returning zero for it
// is indistinguishable from a real region whose checksum happens to be zero, so a
// settle loop polling such a region saw a constant value and declared it stable.
func TestPixelHashBGRA_RejectsOutsideRegion(t *testing.T) {
	const width, height, stride = 4, 3, 20
	data := make([]byte, stride*height)
	for i := range data {
		data[i] = byte(i*37 + 11)
	}

	got, err := find.PixelHashBGRA(data, width, height, stride, image.Rect(8, 8, 9, 9))
	if !errors.Is(err, find.ErrEmptyRegion) {
		t.Fatalf("PixelHashBGRA() error = %v, want ErrEmptyRegion for a region outside the frame", err)
	}
	if got != 0 {
		t.Fatalf("PixelHashBGRA() = %08x alongside an error, want no checksum", got)
	}

	// A region that only partly overlaps still hashes the part that exists.
	if _, err := find.PixelHashBGRA(data, width, height, stride, image.Rect(2, 2, 9, 9)); err != nil {
		t.Fatalf("PixelHashBGRA() on a partly-overlapping region: %v", err)
	}
}

// The packed conversion must not read or write beyond an incomplete pixel,
// even when the destination is shorter than the compositor source row.
func TestCopyBGRAShortDestinationAndPartialPixels(t *testing.T) {
	src := []byte{3, 2, 1, 17, 6, 5, 4, 25}
	for _, tt := range []struct {
		name   string
		source []byte
		dstLen int
		want   []byte
	}{
		{"full", src, 8, []byte{1, 2, 3, 255, 4, 5, 6, 255}},
		{"short destination", src, 4, []byte{1, 2, 3, 255}},
		{"short source", src[:7], 8, []byte{1, 2, 3, 255, 170, 170, 170, 170}},
		{"incomplete pixel", src[:3], 8, []byte{170, 170, 170, 170, 170, 170, 170, 170}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dst := make([]byte, tt.dstLen)
			for i := range dst {
				dst[i] = 170
			}
			copyBGRA(dst, tt.source)
			if !slices.Equal(dst, tt.want) {
				t.Fatalf("copyBGRA(%q) = %v; want %v", tt.name, dst, tt.want)
			}
		})
	}
}

// Empty compositor transfers and absent geometry cannot become valid captures,
// even when the surrounding transport otherwise reports successful delivery.
func TestRawPixelDecodersRejectUnavailableFramesBeforeAllocation(t *testing.T) {
	cases := []struct {
		name                  string
		pixels                []byte
		width, height, stride int
	}{
		{"empty transport", nil, 1, 1, 4},
		{"zero width", []byte{1, 2, 3, 4}, 0, 1, 4},
		{"zero height", []byte{1, 2, 3, 4}, 1, 0, 4},
		{"negative stride", []byte{1, 2, 3, 4}, 1, 1, -4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			full := decodeBGRA(tc.pixels, tc.width, tc.height, tc.stride)
			region := decodeBGRARect(tc.pixels, tc.width, tc.height, tc.stride, image.Rect(0, 0, 1, 1))
			if !full.Bounds().Empty() || !region.Bounds().Empty() {
				t.Fatalf("unavailable frame was decoded: full=%v region=%v", full.Bounds(), region.Bounds())
			}
		})
	}
}
