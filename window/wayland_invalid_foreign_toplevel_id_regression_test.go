package window

import (
	"context"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// The Wayland object ID zero is reserved. A malformed compositor event may
// not publish an unusable window or register a proxy under that ID.
func TestWaylandForeignToplevelRejectsReservedZeroHandle(t *testing.T) {
	for _, wlr := range []bool{false, true} {
		name := "ext"
		if wlr {
			name = "wlroots"
		}
		t.Run(name, func(t *testing.T) {
			manager, wire, _ := newStubWaylandManager("", wlr, false)
			dispatched := false
			wire.dispatchFn = func() error {
				if dispatched {
					return nil
				}
				dispatched = true
				var mgr *wl.RawProxy
				for _, p := range wire.objects {
					if obj, ok := p.(*wl.RawProxy); ok {
						mgr = obj
						break
					}
				}
				if mgr == nil {
					t.Fatal("foreign-toplevel manager not registered")
				}
				mgr.Dispatch(0, -1, []byte{0, 0, 0, 0})
				return nil
			}
			if err := manager.fetchToplevels(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, ok := manager.toplevels[0]; ok {
				t.Fatal("reserved zero ID was published as a window")
			}
			if _, ok := wire.objects[0]; ok {
				t.Fatal("reserved zero ID was registered as a Wayland proxy")
			}
			if manager.changes != nil {
				select {
				case <-manager.changes:
					t.Fatal("invalid event triggered window notification")
				default:
				}
			}
		})
	}
}
