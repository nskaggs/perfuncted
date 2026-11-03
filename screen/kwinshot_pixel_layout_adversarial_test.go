//go:build linux

package screen

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

// KWin sends untrusted byte buffers and stride/format metadata over D-Bus.
// Verify padding bytes never leak into visible pixels and missing rows fail closed.
func TestKWinRawCaptureDecodesRowsWithPaddedStrides(t *testing.T) {
	// Distinct colored pixels, with poison padding after each row.
	for _, tc := range []struct {
		name   string
		format uint32
		data   []byte
	}{
		{"rgba", 17, []byte{10, 20, 30, 255, 90, 91, 92, 93, 40, 50, 60, 255, 94, 95, 96, 97}},
		{"bgra", 4, []byte{30, 20, 10, 255, 90, 91, 92, 93, 60, 50, 40, 255, 94, 95, 96, 97}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := map[string]dbus.Variant{
				"width":  dbus.MakeVariant(uint32(1)),
				"height": dbus.MakeVariant(uint32(2)),
				"stride": dbus.MakeVariant(uint32(8)),
				"format": dbus.MakeVariant(tc.format),
			}
			img, err := decodeKWinPixels(tc.data, image.Rectangle{}, meta)
			if err != nil {
				t.Fatal(err)
			}
			if img.Bounds() != image.Rect(0, 0, 1, 2) {
				t.Fatalf("bounds %v", img.Bounds())
			}
			for y, want := range []color.NRGBA{{10, 20, 30, 255}, {40, 50, 60, 255}} {
				if got := color.NRGBAModel.Convert(img.At(0, y)).(color.NRGBA); got != want {
					t.Fatalf("row %d pixel = %+v, want %+v", y, got, want)
				}
			}
			for _, truncated := range []int{0, 4, 8, 12, 15} {
				if _, err := decodeKWinPixels(tc.data[:truncated], image.Rectangle{}, meta); err == nil || !strings.Contains(err.Error(), "short pixel buffer") {
					t.Fatalf("truncated to %d bytes accepted: %v", truncated, err)
				}
			}
		})
	}
}

func TestKWinPNGPixelCaptureIgnoresUntrustedRawFormatMetadata(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	source.SetNRGBA(0, 0, color.NRGBA{1, 2, 3, 255})
	source.SetNRGBA(1, 0, color.NRGBA{4, 5, 6, 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	// PNG is a supported alternate KWin transport format. Its self-describing
	// dimensions and pixels take precedence over raw QImage format metadata.
	meta := map[string]dbus.Variant{"width": dbus.MakeVariant(uint32(1)), "height": dbus.MakeVariant(uint32(1)), "format": dbus.MakeVariant(uint32(4))}
	got, err := decodeKWinPixels(encoded.Bytes(), image.Rectangle{}, meta)
	if err != nil {
		t.Fatal(err)
	}
	if got.Bounds() != source.Bounds() {
		t.Fatalf("decoded bounds = %v, want %v", got.Bounds(), source.Bounds())
	}
	for x := 0; x < 2; x++ {
		if color.NRGBAModel.Convert(got.At(x, 0)).(color.NRGBA) != color.NRGBAModel.Convert(source.At(x, 0)).(color.NRGBA) {
			t.Fatalf("pixel %d differs: %v != %v", x, got.At(x, 0), source.At(x, 0))
		}
	}
}
