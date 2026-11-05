package window

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// Discovery is transactional: a failed second subscription must not silently
// remove an existing window when the provider reuses an already known ID.
func TestWaylandFailedDiscoveryPreservesExistingCollidingHandle(t *testing.T) {
	for _, tc := range []struct {
		name string
		wlr  bool
	}{{"wlroots", true}, {"ext", false}} {
		t.Run(tc.name, func(t *testing.T) {
			manager, wire, _ := newStubWaylandManager("", tc.wlr, false)
			original := &Info{ID: 77, NativeID: "original", Title: "Editor"}
			manager.toplevels[77] = original
			priorProxy := &wl.RawProxy{}
			wire.SetProxy(77, priorProxy)
			wire.dispatchFn = func() error {
				var p *wl.RawProxy
				for _, obj := range wire.objects {
					if candidate, ok := obj.(*wl.RawProxy); ok && candidate != priorProxy {
						p = candidate
						break
					}
				}
				if p == nil {
					t.Fatal("manager proxy not registered")
				}
				var data [4]byte
				wl.PutUint32(data[:], 77)
				p.Dispatch(0, -1, data[:])
				return errors.New("synthetic setup failed after duplicate handle")
			}
			if err := manager.fetchToplevels(context.Background()); err == nil {
				t.Fatal("expected setup failure")
			}
			if got := manager.toplevels[77]; got != original {
				t.Fatalf("existing window lost after failed discovery: got=%+v", got)
			}
			if got := wire.objects[77]; got != priorProxy {
				t.Fatalf("existing handle proxy lost after failed discovery: got=%T", got)
			}
		})
	}
}
