//go:build linux

package screen

import (
	"context"
	"image"
	"strings"
	"testing"
)

// Invalid geometry is rejected before transport or pipe allocation. This is
// a direct test of KWin's CaptureArea boundary, not the public Grab wrapper.
func TestKWinCaptureAreaRejectsMalformedGeometryBeforeDBus(t *testing.T) {
	transport := &kwinDBusTransport{}
	cases := []struct {
		name string
		rect image.Rectangle
		want string
	}{
		{"empty rectangle", image.Rect(0, 0, 0, 0), "empty rectangle"},
		{"negative width", image.Rectangle{Min: image.Pt(5, 0), Max: image.Pt(2, 1)}, "empty rectangle"},
		{"negative height", image.Rectangle{Min: image.Pt(0, 5), Max: image.Pt(1, 2)}, "empty rectangle"},
		{"x origin underflow", image.Rect(-1<<31-1, 0, -1<<31+1, 1), "x origin"},
		{"x origin overflow", image.Rect(1<<31, 0, 1<<31+1, 1), "x origin"},
		{"y origin underflow", image.Rect(0, -1<<31-1, 1, -1<<31+1), "y origin"},
		{"y origin overflow", image.Rect(0, 1<<31, 1, 1<<31+1), "y origin"},
		{"width overflows uint32", image.Rect(-10, 0, 1<<33, 1), "width"},
		{"height overflows uint32", image.Rect(0, -10, 1, 1<<33), "height"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			img, err := transport.CaptureArea(context.Background(), tc.rect)
			if img != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid capture geometry %+v gave image=%v err=%v", tc.rect, img, err)
			}
		})
	}
}
