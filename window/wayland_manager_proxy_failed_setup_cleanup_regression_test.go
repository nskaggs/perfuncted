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
	bindFailure := errors.New("synthetic manager bind failure")
	roundTripFailure := errors.New("synthetic transport failure")
	for _, tc := range []struct {
		name      string
		configure func(*WaylandWindowManager, *mockWaylandContext)
		wantErr   error
	}{
		{"no protocol", func(m *WaylandWindowManager, _ *mockWaylandContext) { m.wlrMgrID = 0; m.extMgrID = 0 }, nil},
		{"manager bind failed", func(m *WaylandWindowManager, _ *mockWaylandContext) {
			registry, ok := m.registry.(*mockRegistry)
			if !ok {
				t.Fatalf("registry fixture type = %T", m.registry)
			}
			m.registry = &failingWaylandRegistry{mockRegistry: registry, err: bindFailure}
		}, bindFailure},
		{"round trip failed", func(_ *WaylandWindowManager, c *mockWaylandContext) {
			c.dispatchFn = func() error { return roundTripFailure }
		}, roundTripFailure},
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
			} else if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("setup error = %v, want wrapped %v", err, tc.wantErr)
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

type failingWaylandRegistry struct {
	*mockRegistry
	err error
}

func (r *failingWaylandRegistry) BindContext(context.Context, uint32, string, uint32, uint32) error {
	return r.err
}
