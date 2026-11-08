//go:build linux

package window

import (
	"encoding/binary"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// A GetProperty response with BytesAfter set is only a prefix. Do not expose
// a supposedly complete EWMH state or WM_CLASS from such a reply.
func TestX11WindowStateRejectsTruncatedAtomList(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(_ bool, _ xproto.Window, p, _ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
		if p != b.atomNetWMState {
			t.Fatalf("unexpected property %d", p)
		}
		v := make([]byte, 4)
		binary.LittleEndian.PutUint32(v, uint32(b.atomNetWMStateFullscreen))
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: v, BytesAfter: 4})
	}
	minimized, maximized, fullscreen := b.windowState(50)
	if minimized || maximized || fullscreen {
		t.Fatalf("truncated atom list exposed state: %v %v %v", minimized, maximized, fullscreen)
	}
}

func TestX11WindowClassRejectsTruncatedProperty(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(_ bool, _ xproto.Window, p, typ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
		if p != xproto.AtomWmClass || typ != xproto.AtomString {
			t.Fatalf("unexpected property/type %d/%d", p, typ)
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 8, Value: []byte("trusted\x00TrustedApp\x00"), BytesAfter: 4})
	}
	instance, class := b.windowClass(50)
	if instance != "" || class != "" {
		t.Fatalf("truncated WM_CLASS exposed %q/%q", instance, class)
	}
}
