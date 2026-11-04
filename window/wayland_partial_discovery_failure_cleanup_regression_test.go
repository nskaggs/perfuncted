package window

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// A compositor can advertise handles and still fail its setup round trip.
// Those provisional handles must not outlive their rejected subscription.
func TestWaylandFailedPartialDiscoveryReleasesProvisionalHandles(t *testing.T) {
	for _, tc := range []struct {
		name string
		wlr  bool
	}{{"wlroots", true}, {"ext", false}} {
		t.Run(tc.name, func(t *testing.T) {
			m, wire, _ := newStubWaylandManager("", tc.wlr, false)
			existing := &Info{ID: 55, Title: "existing"}
			m.toplevels[55] = existing
			dispatched := false
			wire.dispatchFn = func() error {
				if dispatched {
					return errors.New("synthetic setup failure")
				}
				dispatched = true
				var manager *wl.RawProxy
				for _, p := range wire.objects {
					if proxy, ok := p.(*wl.RawProxy); ok {
						manager = proxy
						break
					}
				}
				if manager == nil {
					t.Fatal("missing manager proxy")
				}
				var id [4]byte
				wl.PutUint32(id[:], 77)
				manager.Dispatch(0, -1, id[:])
				if _, ok := wire.objects[77]; !ok {
					t.Fatal("synthetic handle was not registered")
				}
				return errors.New("synthetic setup failure")
			}
			if err := m.fetchToplevels(context.Background()); err == nil {
				t.Fatal("failed round trip succeeded")
			}
			if _, ok := wire.objects[77]; ok {
				t.Fatal("failed discovery leaked provisional Wayland proxy")
			}
			if _, ok := m.toplevels[77]; ok {
				t.Fatal("failed discovery published provisional window")
			}
			if got := m.toplevels[55]; got != existing {
				t.Fatalf("unrelated window corrupted: %+v", got)
			}
		})
	}
}
