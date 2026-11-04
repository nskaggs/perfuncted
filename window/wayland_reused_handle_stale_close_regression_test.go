package window

import "testing"

// Foreign-toplevel object IDs are reusable after the old Wayland proxy is
// retired. A delayed callback still references the original Info pointer and
// must not delete the replacement when its old close notification arrives.
func TestWaylandStaleHandleCloseDoesNotRemoveReplacement(t *testing.T) {
	for _, tc := range []struct {
		name        string
		closeOpcode uint32
		titleOpcode uint32
		deliver     func(*WaylandWindowManager, uint32, *Info, uint32, []byte)
	}{
		{"wlroots", 6, 0, (*WaylandWindowManager).handleWLRToplevelEvent},
		{"ext", 0, 2, (*WaylandWindowManager).handleExtToplevelEvent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := &WaylandWindowManager{toplevels: make(map[uint32]*Info)}
			old := &Info{ID: 42, NativeID: "old", Title: "Old"}
			replacement := &Info{ID: 42, NativeID: "new", Title: "New"}
			manager.toplevels[42] = old
			// Represent a second manager creation event after the old object
			// was retired. The old callback can still be delivered late.
			manager.toplevels[42] = replacement
			// Stale metadata must also be ignored rather than waking discovery
			// observers or publishing attributes from a retired object.
			staleTitle := []byte{6, 0, 0, 0, 'S', 't', 'a', 'l', 'e', 0}
			tc.deliver(manager, 42, old, tc.titleOpcode, staleTitle)
			if old.Title != "Old" || replacement.Title != "New" {
				t.Fatalf("stale event mutated toplevel metadata: old=%+v new=%+v", old, replacement)
			}
			if manager.changes != nil {
				select {
				case <-manager.changes:
					t.Fatal("stale event falsely signaled a window change")
				default:
				}
			}
			tc.deliver(manager, 42, old, tc.closeOpcode, nil)
			got, ok := manager.toplevels[42]
			if !ok || got != replacement || got.Title != "New" {
				t.Fatalf("stale close retired replacement handle: present=%v got=%+v", ok, got)
			}
			tc.deliver(manager, 42, replacement, tc.closeOpcode, nil)
			if len(manager.toplevels) != 0 {
				t.Fatalf("current handle close was ignored: %+v", manager.toplevels)
			}
		})
	}
}
