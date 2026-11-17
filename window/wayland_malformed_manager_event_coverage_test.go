package window

import (
	"context"
	"fmt"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func TestWaylandForeignToplevelIgnoresMalformedManagerEvents(t *testing.T) {
	manager, wire, _ := newStubWaylandManager("", true, false)
	managerProxyID := wire.nextID + 1
	wire.dispatchFn = func() error {
		proxy, ok := wire.objects[managerProxyID].(*wl.RawProxy)
		if !ok {
			return fmt.Errorf("foreign-toplevel manager proxy %d is unavailable", managerProxyID)
		}
		proxy.Dispatch(1, -1, []byte{77, 0, 0, 0})
		proxy.Dispatch(0, -1, []byte{77, 0, 0})
		return nil
	}
	if err := manager.fetchToplevels(context.Background()); err != nil {
		t.Fatalf("fetch empty compositor snapshot: %v", err)
	}
	windows, err := manager.List(context.Background())
	if err != nil || len(windows) != 0 {
		t.Fatalf("malformed manager events published windows: %+v, %v", windows, err)
	}
}
