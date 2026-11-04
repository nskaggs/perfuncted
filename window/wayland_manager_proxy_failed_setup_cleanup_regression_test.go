package window

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// A rejected foreign-toplevel setup is not an active subscription. It must
// not leave a manager proxy in the Wayland object registry after failure.
func TestWaylandFailedForeignToplevelSetupUnregistersManagerProxy(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*WaylandWindowManager, *mockWaylandContext)
	}{
		{"no protocol", func(m *WaylandWindowManager, _ *mockWaylandContext) { m.wlrMgrID = 0; m.extMgrID = 0 }},
		{"round trip failed", func(_ *WaylandWindowManager, c *mockWaylandContext) {
			c.dispatchFn = func() error { return errors.New("synthetic transport failure") }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, wire, _ := newStubWaylandManager("", true, false)
			tc.configure(m, wire)
			managerProxyID := wire.nextID + 1
			previous := make(map[uint32]wl.Proxy, len(wire.objects))
			for id, proxy := range wire.objects {
				previous[id] = proxy
			}
			if err := m.fetchToplevels(context.Background()); err == nil {
				t.Fatal("broken setup unexpectedly succeeded")
			}
			if _, leaked := wire.objects[managerProxyID]; leaked {
				t.Fatalf("failed setup leaked foreign-toplevel manager proxy %d", managerProxyID)
			}
			for id, proxy := range previous {
				if wire.objects[id] != proxy {
					t.Fatalf("failed setup replaced preexisting Wayland object %d", id)
				}
			}
		})
	}
}
