package window

import (
	"context"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// Foreign-toplevel new_id must not overwrite other already-registered objects,
// including the Wayland registry and the seat used for activation.
func TestWaylandForeignToplevelRejectsOtherExistingProtocolIdentities(t *testing.T) {
	for _, wlr := range []bool{false, true} {
		protocol := "ext"
		if wlr {
			protocol = "wlroots"
		}
		for _, identity := range []string{"registry", "seat"} {
			t.Run(protocol+"/"+identity, func(t *testing.T) {
				m, wire, _ := newStubWaylandManager("", wlr, true)
				var id uint32
				if identity == "registry" {
					id = m.registry.ID()
				} else {
					id = m.seat.ID()
				}
				original := wire.objects[id]
				if original == nil {
					t.Fatalf("missing %s proxy %d", identity, id)
				}
				dispatched := false
				wire.dispatchFn = func() error {
					if dispatched {
						return nil
					}
					dispatched = true
					var manager *wl.RawProxy
					for proxyID, candidate := range wire.objects {
						if proxy, ok := candidate.(*wl.RawProxy); ok && proxyID != m.seat.ID() {
							manager = proxy
							break
						}
					}
					if manager == nil {
						t.Fatal("missing foreign-toplevel manager proxy")
					}
					var announcement [4]byte
					wl.PutUint32(announcement[:], id)
					manager.Dispatch(0, -1, announcement[:])
					return nil
				}
				if err := m.fetchToplevels(context.Background()); err != nil {
					t.Fatal(err)
				}
				if wire.objects[id] != original {
					t.Fatalf("foreign-toplevel replaced %s proxy %d", identity, id)
				}
				if _, exists := m.toplevels[id]; exists {
					t.Fatalf("foreign-toplevel published aliased %s object", identity)
				}
			})
		}
	}
}
