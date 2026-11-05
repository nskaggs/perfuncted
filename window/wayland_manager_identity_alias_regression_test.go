package window

import (
	"context"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// A malicious compositor must not be able to reassign the foreign-toplevel
// manager's existing protocol object identity to an advertised window.
// Replacing the manager proxy would strand its event handler and break
// subsequent window discovery even when the malformed event is ignored.
func TestWaylandForeignToplevelRejectsManagerObjectIDAlias(t *testing.T) {
	for _, wlr := range []bool{false, true} {
		label := "ext"
		if wlr {
			label = "wlroots"
		}
		t.Run(label, func(t *testing.T) {
			manager, wire, _ := newStubWaylandManager("", wlr, false)
			delivered := false
			var original *wl.RawProxy
			wire.dispatchFn = func() error {
				if delivered {
					return nil
				}
				delivered = true
				for _, p := range wire.objects {
					if proxy, ok := p.(*wl.RawProxy); ok {
						original = proxy
						break
					}
				}
				if original == nil {
					t.Fatal("no manager proxy registered")
				}
				var id [4]byte
				wl.PutUint32(id[:], original.ID())
				original.Dispatch(0, -1, id[:])
				return nil
			}
			if err := manager.fetchToplevels(context.Background()); err != nil {
				t.Fatal(err)
			}
			if original == nil {
				t.Fatal("manager was never dispatched")
			}
			if current := wire.objects[original.ID()]; current != original {
				t.Fatalf("compositor replaced manager protocol proxy %d: %T", original.ID(), current)
			}
			if _, exists := manager.toplevels[original.ID()]; exists {
				t.Fatal("aliased manager ID published as toplevel")
			}
			if len(manager.toplevels) != 0 {
				t.Fatalf("unexpected windows: %v", manager.toplevels)
			}
		})
	}
}
