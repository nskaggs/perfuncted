package window

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// The compositor may retain a callback reference after an aborted initial
// round trip. A rejected subscription cannot subsequently publish windows.
func TestWaylandFailedSubscriptionCannotPublishFromLateCallback(t *testing.T) {
	for _, tc := range []struct {
		name string
		wlr  bool
	}{{"wlroots", true}, {"ext", false}} {
		t.Run(tc.name, func(t *testing.T) {
			m, wire, _ := newStubWaylandManager("", tc.wlr, false)
			var stale *wl.RawProxy
			wire.dispatchFn = func() error {
				for _, p := range wire.objects {
					if proxy, ok := p.(*wl.RawProxy); ok {
						stale = proxy
						break
					}
				}
				return errors.New("synthetic compositor setup failure")
			}
			if err := m.fetchToplevels(context.Background()); err == nil {
				t.Fatal("expected setup failure")
			}
			if stale == nil {
				t.Fatal("failed discovery did not expose a manager callback")
			}
			var id [4]byte
			wl.PutUint32(id[:], 77)
			stale.Dispatch(0, -1, id[:])
			if _, ok := m.toplevels[77]; ok {
				t.Fatal("late event from failed subscription published a window")
			}
			if _, ok := wire.objects[77]; ok {
				t.Fatal("late event from failed subscription registered a proxy")
			}
			if m.changes != nil {
				select {
				case <-m.changes:
					t.Fatal("late failed subscription emitted a window notification")
				default:
				}
			}
		})
	}
}
