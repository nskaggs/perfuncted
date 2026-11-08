//go:build linux

package window

import (
	"encoding/binary"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// BytesAfter marks an incomplete GetProperty result, even if the first chunk
// has the expected shape. Never expose a partial active ID or process ID.
func TestX11ScalarMetadataRejectsTruncatedReplies(t *testing.T) {
	for _, tc := range []struct {
		name   string
		active bool
	}{
		{"active window", true}, {"process ID", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			conn.GetPropertyFunc = func(_ bool, _ xproto.Window, p, _ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
				want := b.atomNetWMPID
				if tc.active {
					want = b.atomNetActiveWindow
				}
				if p != want {
					t.Fatalf("unexpected property %d", p)
				}
				v := make([]byte, 4)
				binary.LittleEndian.PutUint32(v, 50)
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: v, BytesAfter: 4})
			}
			if tc.active {
				got, err := b.activeWindow()
				if err != nil || got != 0 {
					t.Fatalf("truncated active window got=%d err=%v", got, err)
				}
			} else if got := b.windowPID(50); got != 0 {
				t.Fatalf("truncated PID got=%d", got)
			}
		})
	}
}

// Incomplete hints must not suppress decorations or enlarge geometry.
func TestX11DecorationsRejectTruncatedReplies(t *testing.T) {
	t.Run("motif hints", func(t *testing.T) {
		b, conn := newStubX11Backend(t, false, "")
		conn.GetPropertyFunc = func(_ bool, _ xproto.Window, p, _ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
			if p != b.atomMotifWMHints {
				t.Fatalf("unexpected property %d", p)
			}
			v := make([]byte, 20)
			binary.LittleEndian.PutUint32(v, 2)
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: v, BytesAfter: 4})
		}
		if !b.windowHasDecoration(50) {
			t.Fatal("truncated Motif hints suppressed window decorations")
		}
	})
	t.Run("frame extents", func(t *testing.T) {
		b, conn := newStubX11Backend(t, true, "Window")
		prev := conn.GetPropertyFunc
		conn.GetPropertyFunc = func(del bool, w xproto.Window, p, typ xproto.Atom, offset, length uint32) x11.GetPropertyCookie {
			if p == b.atomNetFrameExtent {
				v := make([]byte, 16)
				for i, e := range []uint32{14, 9, 30, 19} {
					binary.LittleEndian.PutUint32(v[i*4:], e)
				}
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: v, BytesAfter: 4})
			}
			return prev(del, w, p, typ, offset, length)
		}
		x, y, width, height := b.windowGeometry(50)
		if x != 10 || y != 20 || width != 800 || height != 600 {
			t.Fatalf("truncated frame extents changed geometry (%d,%d,%d,%d)", x, y, width, height)
		}
	})
}
