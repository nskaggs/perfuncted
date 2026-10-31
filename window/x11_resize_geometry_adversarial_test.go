package window

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

func TestX11ResizeRejectsInvalidDimensionsBeforeSendingProtocol(t *testing.T) {
	for _, tc := range []struct {
		name string
		w, h int
	}{
		{"zero width", 0, 20}, {"negative width", -1, 20}, {"zero height", 30, 0}, {"negative height", 30, -1}, {"width exceeds wire size", 65536, 40}, {"height exceeds wire size", 50, 65536},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, true, "A")
			invoked := 0
			conn.ConfigureWindowCheckedFunc = func(xproto.Window, uint16, []uint32) x11.ConfigureWindowCookie {
				invoked++
				return &x11.MockCheckCookie{}
			}
			err := b.ResizeByID(context.Background(), "50", tc.w, tc.h)
			if err == nil || invoked != 0 {
				t.Fatalf("X11 accepted invalid %dx%d resize: %v, %d mutations", tc.w, tc.h, err, invoked)
			}
		})
	}
}

func TestX11GeometryIncludesFrameOnlyForDecoratedWindows(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		flags, decorations         uint32
		frameErr                   error
		wantX, wantY, wantW, wantH int
	}{
		{"decorated window", 2, 1, nil, 86, 170, 323, 249},
		{"CSD no decoration", 2, 0, nil, 100, 200, 300, 200},
		{"no motif hint defaults decorated", 0, 0, nil, 86, 170, 323, 249},
		{"missing extent leaves client geometry", 2, 1, errors.New("no extents"), 100, 200, 300, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			conn.GetGeometryFunc = func(xproto.Drawable) x11.GetGeometryCookie {
				return x11.NewMockGetGeometryCookie(&xproto.GetGeometryReply{X: 5, Y: 7, Width: 300, Height: 200})
			}
			conn.TranslateCoordinatesFunc = func(xproto.Window, xproto.Window, int16, int16) x11.TranslateCoordinatesCookie {
				return x11.NewMockTranslateCoordinatesCookie(&xproto.TranslateCoordinatesReply{DstX: 100, DstY: 200})
			}
			conn.GetPropertyFunc = func(_ bool, _ xproto.Window, p, _ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
				if p == b.atomMotifWMHints {
					data := make([]byte, 20)
					binary.LittleEndian.PutUint32(data, tc.flags)
					binary.LittleEndian.PutUint32(data[8:], tc.decorations)
					return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: data})
				}
				if p == b.atomNetFrameExtent {
					if tc.frameErr != nil {
						return x11.NewMockGetPropertyCookieError(tc.frameErr)
					}
					data := make([]byte, 16)
					for i, v := range []uint32{14, 9, 30, 19} {
						binary.LittleEndian.PutUint32(data[i*4:], v)
					}
					return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: data})
				}
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{})
			}
			x, y, w, h := b.windowGeometry(50)
			if x != tc.wantX || y != tc.wantY || w != tc.wantW || h != tc.wantH {
				t.Fatalf("frame geometry (%d,%d,%d,%d) want (%d,%d,%d,%d)", x, y, w, h, tc.wantX, tc.wantY, tc.wantW, tc.wantH)
			}
		})
	}
}

func TestX11TitleFallsBackToLegacyWMNameWithoutLeakingInvalidUTF8Property(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	names := []string{}
	conn.GetPropertyFunc = func(_ bool, _ xproto.Window, p, _ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
		switch p {
		case b.atomNetWMName:
			names = append(names, "UTF8")
			return x11.NewMockGetPropertyCookieError(errors.New("UTF8 unsupported"))
		case xproto.AtomWmName:
			names = append(names, "WM_NAME")
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 8, Value: []byte("Fallback")})
		default:
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{})
		}
	}
	if name := b.windowTitle(50); name != "Fallback" {
		t.Fatalf("fallback title %q", name)
	}
	if len(names) != 2 || names[0] != "UTF8" || names[1] != "WM_NAME" {
		t.Fatalf("unexpected title resolution order: %v", names)
	}
}
