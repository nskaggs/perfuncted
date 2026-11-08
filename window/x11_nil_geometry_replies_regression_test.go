//go:build linux

package window

import (
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// An X11 round trip may return a missing reply without a transport error.
// Geometry discovery must degrade safely instead of panicking.
func TestX11GeometryHandlesMissingGeometryAndTranslationReplies(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		nilGeometry                bool
		wantX, wantY, wantW, wantH int
	}{
		{"missing geometry reply", true, 0, 0, 0, 0},
		{"missing translated coordinates", false, 10, 20, 800, 600},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			if tc.nilGeometry {
				conn.GetGeometryFunc = func(xproto.Drawable) x11.GetGeometryCookie { return x11.NewMockGetGeometryCookie(nil) }
			} else {
				conn.GetGeometryFunc = func(xproto.Drawable) x11.GetGeometryCookie {
					return x11.NewMockGetGeometryCookie(&xproto.GetGeometryReply{X: 10, Y: 20, Width: 800, Height: 600})
				}
				conn.TranslateCoordinatesFunc = func(xproto.Window, xproto.Window, int16, int16) x11.TranslateCoordinatesCookie {
					return x11.NewMockTranslateCoordinatesCookie(nil)
				}
			}
			x, y, w, h := b.windowGeometry(50)
			if x != tc.wantX || y != tc.wantY || w != tc.wantW || h != tc.wantH {
				t.Fatalf("geometry (%d,%d,%d,%d) want (%d,%d,%d,%d)", x, y, w, h, tc.wantX, tc.wantY, tc.wantW, tc.wantH)
			}
		})
	}
}
