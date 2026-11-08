package output

import (
	"context"
	"strings"
	"testing"

	"github.com/jezek/xgb/randr"
	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// The root fallback represents a drawable screen. A zero-sized root from a
// broken X server is not an available display and must not be advertised.
func TestX11ListerRejectsZeroSizedRootGeometry(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height uint16
		randr         bool
	}{
		{"missing width no RandR", 0, 1080, false},
		{"missing height no RandR", 1920, 0, false},
		{"zero root RandR unavailable", 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := &x11.MockConnection{DefaultScreenFunc: func() *xproto.ScreenInfo {
				return &xproto.ScreenInfo{Root: 99, WidthInPixels: tc.width, HeightInPixels: tc.height}
			}}
			if tc.randr {
				conn.GetScreenResourcesCurrentFunc = func(xproto.Window) x11.RandRScreenResourcesCookie {
					return x11.NewMockRandRScreenResourcesCookie(&randr.GetScreenResourcesCurrentReply{}, nil)
				}
			}
			lister := &X11Lister{conn: conn, randrAvailable: tc.randr}
			outputs, err := lister.List(context.Background())
			if err == nil || len(outputs) != 0 || !strings.Contains(err.Error(), "root screen") {
				t.Fatalf("invalid root geometry exposed outputs=%+v err=%v", outputs, err)
			}
		})
	}
}
