package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
)

// Verify the complete image that a consumer reconstructs, rather than only
// inspecting individual crops. A consumer must discard the old base on every
// full frame, including a geometry change and a rectangle-budget resync.
func TestReleaseConsumerReconstructsScreenStream(t *testing.T) {
	samples, expected := releaseConsumerSamples()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wire bytes.Buffer
	next := 0
	capture := func(context.Context, image.Rectangle) (perfuncted.Capture, error) {
		if next == len(samples) {
			cancel()
			return perfuncted.Capture{}, ctx.Err()
		}
		sample := samples[next]
		next++
		return sample, nil
	}
	if err := watchScreenChanges(ctx, &wire, samples[0].ScreenRect, time.Nanosecond, capture, true); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&wire)
	var reconstructed *image.NRGBA
	for i, want := range expected {
		var frame watchChangeFrame
		if err := decoder.Decode(&frame); err != nil {
			t.Fatalf("frame %d: %v", i+1, err)
		}
		checkReleaseConsumerHeader(t, frame, want, uint64(i+1))
		reconstructed = applyReleaseConsumerFrame(t, reconstructed, frame)
		assertReleaseConsumerImage(t, reconstructed, want.capture.Image)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("unchanged samples must not emit frames: %s, %v", extra, err)
	}
}

type releaseConsumerExpectation struct {
	capture perfuncted.Capture
	version uint64
	full    bool
}

func releaseConsumerSamples() ([]perfuncted.Capture, []releaseConsumerExpectation) {
	region := image.Rect(10, 20, 30, 40)
	base := releaseConsumerSolid(image.Rect(5, 7, 25, 27), color.NRGBA{R: 80, A: 255})
	sparse := releaseConsumerClone(base)
	sparse.SetNRGBA(6, 9, color.NRGBA{B: 255, A: 255})
	sparse.SetNRGBA(22, 24, color.NRGBA{G: 255, A: 255})

	// A nonzero pixel origin and changed scale both require a new base.
	resized := releaseConsumerSolid(image.Rect(-3, 9, 37, 49), color.NRGBA{R: 80, A: 255})
	scaledDelta := releaseConsumerClone(resized)
	scaledDelta.SetNRGBA(10, 15, color.NRGBA{B: 255, A: 255})

	// 800 isolated differences overflow the 256-rectangle budget.
	overflow := releaseConsumerClone(scaledDelta)
	for y := overflow.Rect.Min.Y; y < overflow.Rect.Max.Y; y++ {
		for x := overflow.Rect.Min.X; x < overflow.Rect.Max.X; x++ {
			if (x-overflow.Rect.Min.X+y-overflow.Rect.Min.Y)%2 == 0 {
				overflow.SetNRGBA(x, y, color.NRGBA{G: 255, A: 255})
			}
		}
	}
	lastDelta := releaseConsumerClone(overflow)
	lastDelta.SetNRGBA(0, 11, color.NRGBA{R: 255, G: 255, A: 255})
	translated := region.Add(image.Pt(100, 60))

	want := []releaseConsumerExpectation{
		{perfuncted.Capture{Image: base, ScreenRect: region}, 1, true},
		{perfuncted.Capture{Image: sparse, ScreenRect: region}, 1, false},
		{perfuncted.Capture{Image: resized, ScreenRect: region}, 2, true},
		{perfuncted.Capture{Image: scaledDelta, ScreenRect: region}, 2, false},
		{perfuncted.Capture{Image: overflow, ScreenRect: region}, 2, true},
		{perfuncted.Capture{Image: lastDelta, ScreenRect: region}, 2, false},
		{perfuncted.Capture{Image: lastDelta, ScreenRect: translated}, 3, true},
	}
	samples := make([]perfuncted.Capture, 0, len(want)*2)
	for _, entry := range want {
		// A stable sample after each change must never advance the frame ID.
		samples = append(samples, entry.capture, entry.capture)
	}
	return samples, want
}

func releaseConsumerSolid(bounds image.Rectangle, c color.NRGBA) *image.NRGBA {
	// Use a padded backing image, not just tightly packed pixels.
	outer := image.NewNRGBA(bounds.Inset(-3))
	result, ok := outer.SubImage(bounds).(*image.NRGBA)
	if !ok {
		panic("unexpected NRGBA subimage type")
	}
	draw.Draw(result, bounds, image.NewUniform(c), image.Point{}, draw.Src)
	return result
}

func releaseConsumerClone(src *image.NRGBA) *image.NRGBA {
	dst := image.NewNRGBA(src.Bounds())
	draw.Draw(dst, dst.Bounds(), src, src.Bounds().Min, draw.Src)
	return dst
}

func checkReleaseConsumerHeader(t *testing.T, got watchChangeFrame, want releaseConsumerExpectation, id uint64) {
	t.Helper()
	if got.FrameID != id || got.GeometryVersion != want.version || got.FullFrame != want.full {
		t.Fatalf("frame %d: incorrect identity, version or full-frame flag: %+v", id, got)
	}
	if got.ImageBounds != want.capture.Image.Bounds() || got.ScreenRect != want.capture.ScreenRect || got.Timestamp.IsZero() {
		t.Fatalf("frame %d: incorrect geometry or timestamp", id)
	}
	if want.full {
		if got.BaseFrameID != nil || len(got.Pixels) != 1 || got.Pixels[0].ImageBounds != got.ImageBounds {
			t.Fatalf("frame %d: incomplete full-frame resync", id)
		}
		return
	}
	if got.BaseFrameID == nil || *got.BaseFrameID != id-1 || len(got.Pixels) == 0 {
		t.Fatalf("frame %d: incorrect delta base", id)
	}
}

func applyReleaseConsumerFrame(t *testing.T, current *image.NRGBA, frame watchChangeFrame) *image.NRGBA {
	t.Helper()
	if frame.FullFrame {
		current = image.NewNRGBA(frame.ImageBounds)
	}
	if current == nil || current.Bounds() != frame.ImageBounds {
		t.Fatal("delta cannot be applied to the current base")
	}
	for _, payload := range frame.Pixels {
		crop := payload.ImageBounds
		scale := float64(frame.ImageBounds.Dx()) / float64(frame.ScreenRect.Dx())
		if crop.Empty() || !crop.In(current.Bounds()) || payload.Encoding != "png+base64" ||
			payload.Width != crop.Dx() || payload.Height != crop.Dy() ||
			payload.GeometryVersion != frame.GeometryVersion || payload.PixelScale != scale {
			t.Fatalf("invalid crop metadata: %+v", payload)
		}
		decoded, err := png.Decode(bytes.NewReader(payload.Data))
		if err != nil {
			t.Fatal(err)
		}
		if decoded.Bounds().Size() != crop.Size() {
			t.Fatalf("decoded PNG size %v does not match %v", decoded.Bounds().Size(), crop.Size())
		}
		draw.Draw(current, crop, decoded, decoded.Bounds().Min, draw.Src)
	}
	return current
}

func assertReleaseConsumerImage(t *testing.T, got *image.NRGBA, want image.Image) {
	t.Helper()
	if got.Bounds() != want.Bounds() {
		t.Fatalf("reconstructed bounds %v, want %v", got.Bounds(), want.Bounds())
	}
	for y := want.Bounds().Min.Y; y < want.Bounds().Max.Y; y++ {
		for x := want.Bounds().Min.X; x < want.Bounds().Max.X; x++ {
			if got.NRGBAAt(x, y) != color.NRGBAModel.Convert(want.At(x, y)) {
				t.Fatalf("incorrect reconstructed pixel at (%d,%d)", x, y)
			}
		}
	}
}
