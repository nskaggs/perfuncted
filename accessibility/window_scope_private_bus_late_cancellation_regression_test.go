package accessibility

import (
	"context"
	"errors"
	"testing"
)

// The AT-SPI provider is allowed to answer GetChildren after the caller
// cancels. A window scope must not be published from that stale reply.
func TestSyntheticATSPICorrelationRejectsCancellationDuringDesktopChildrenRead(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		name := "cached"
		if fresh {
			name = "fresh"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backend, _, target := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, frame *syntheticATSPINode) {
				root.childrenHook = cancel
			})
			var err error
			var scope WindowScope
			if fresh {
				scope, err = backend.ResolveWindowFresh(ctx, target)
			} else {
				scope, err = backend.ResolveWindow(ctx, target)
			}
			if !errors.Is(err, context.Canceled) || scope.Root.BusName != "" {
				t.Fatalf("canceled provider response published scope %+v with err %v", scope, err)
			}
		})
	}
}
