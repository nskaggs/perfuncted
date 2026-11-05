package window

import (
	"context"
	"github.com/nskaggs/perfuncted/internal/wl"
	"testing"
)

// Retired Wayland callbacks may still be dispatched by a buffered reader;
// after shutdown none may revive the manager's window state.
func TestWaylandLateToplevelEventsCannotReviveClosedManager(t *testing.T) {
	for _, tc := range []struct {
		name    string
		wlr     bool
		titleOp uint32
	}{{"wlroots", true, 0}, {"ext", false, 2}} {
		t.Run(tc.name, func(t *testing.T) {
			m, wire, _ := newStubWaylandManager("", tc.wlr, false)
			if err := m.fetchToplevels(context.Background()); err != nil {
				t.Fatal(err)
			}
			var mgr *wl.RawProxy
			for _, obj := range wire.objects {
				if p, ok := obj.(*wl.RawProxy); ok {
					mgr = p
					break
				}
			}
			if mgr == nil {
				t.Fatal("no manager callback")
			}
			var id [4]byte
			wl.PutUint32(id[:], 77)
			mgr.Dispatch(0, -1, id[:])
			handle, ok := wire.objects[77].(*wl.RawProxy)
			if !ok {
				t.Fatal("no window callback")
			}
			old := m.toplevels[77]
			if err := m.Close(); err != nil {
				t.Fatal(err)
			}
			data := []byte{5, 0, 0, 0, 'L', 'a', 't', 'e', 0}
			handle.Dispatch(tc.titleOp, -1, data)
			if old.Title != "" {
				t.Fatalf("closed manager accepted late title: %q", old.Title)
			}
			wl.PutUint32(id[:], 88)
			mgr.Dispatch(0, -1, id[:])
			if _, ok := m.toplevels[88]; ok {
				t.Fatal("closed manager published a late new window")
			}
			if _, ok := wire.objects[88]; ok {
				t.Fatal("closed manager registered a late proxy")
			}
		})
	}
}
