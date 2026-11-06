//go:build linux

package screen

import (
	"context"
	"errors"
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/find"
)

// A tightly packed QImage RGBA8888 buffer must not take the BGRA conversion
// path. The decoder owns the returned frame and preserves the pixel channels,
// including a nonopaque alpha value, without leaking transport metadata.
func TestKWinPackedRGBA8888PreservesTransparentPixels(t *testing.T) {
	pixels := []byte{8, 27, 46, 255, 91, 83, 72, 128, 1, 2, 3, 64, 255, 201, 34, 0}
	metadata := map[string]dbus.Variant{
		"width": dbus.MakeVariant(uint32(2)), "height": dbus.MakeVariant(uint32(2)),
		"stride": dbus.MakeVariant(uint32(8)), "format": dbus.MakeVariant(uint32(17)),
	}
	img, err := decodeKWinPixels(pixels, image.Rectangle{}, metadata)
	if err != nil {
		t.Fatal(err)
	}
	decoded, ok := img.(*image.NRGBA)
	if !ok {
		t.Fatalf("packed RGBA decoded into unexpected type %T", img)
	}
	want := []color.NRGBA{{8, 27, 46, 255}, {91, 83, 72, 128}, {1, 2, 3, 64}, {255, 201, 34, 0}}
	for i, expected := range want {
		x, y := i%2, i/2
		got := decoded.NRGBAAt(x, y)
		if got != expected {
			t.Fatalf("packed RGBA pixel %d changed: got %+v want %+v", i, got, expected)
		}
	}
	// Changing the transport buffer after decoding cannot change the image.
	for i := range pixels {
		pixels[i] = 0
	}
	if got := decoded.NRGBAAt(0, 0); got != want[0] {
		t.Fatalf("decoded image retained transport memory: %+v", got)
	}
}

func TestKWinEmptyRegionHashUsesAuthorizedFullScreenCapture(t *testing.T) {
	source := solidRGBAForKWin(color.RGBA{R: 20, G: 30, B: 40, A: 255}, 3, 2)
	transport := &fakeKWinTransport{active: source}
	backend := &KWinShotBackend{transport: transport}
	hash, err := backend.GrabRegionHash(context.Background(), image.Rectangle{})
	if err != nil || hash != find.PixelHash(source, nil) {
		t.Fatalf("empty region hash=%08x err=%v", hash, err)
	}
	if transport.activeCalls != 1 || transport.areaCalls != 0 {
		t.Fatalf("empty region used wrong screenshot authority: %+v", transport)
	}
}

func TestKWinTransportRejectsInvalidCaptureBeforeCallingDBus(t *testing.T) {
	transport := &kwinDBusTransport{}
	if img, err := transport.CaptureArea(context.Background(), image.Rectangle{}); img != nil || err == nil || !strings.Contains(err.Error(), "empty rectangle") {
		t.Fatalf("empty capture area reached transport: %v %v", img, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if img, err := transport.CaptureActiveScreen(ctx); img != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled capture reached transport: %v %v", img, err)
	}
}

func TestKWinRawPixelDecodeSupportsRegionWithoutMetadata(t *testing.T) {
	rect := image.Rect(10, 20, 11, 21)
	img, err := decodeKWinPixels([]byte{33, 22, 11, 255}, rect, nil)
	if err != nil {
		t.Fatalf("raw BGRA without metadata: %v", err)
	}
	decoded, ok := img.(*image.NRGBA)
	if !ok {
		t.Fatalf("raw BGRA decoded into unexpected type %T", img)
	}
	if got := decoded.NRGBAAt(0, 0); got != (color.NRGBA{11, 22, 33, 255}) {
		t.Fatalf("raw capture without metadata changed channels: %+v", got)
	}
}
