package accessibility

import (
	"context"
	"errors"
	"testing"
)

// An active-only window target cannot be matched to an AT-SPI frame merely
// because the application matches and only one top-level frame is available.
// The candidate must actually report focused/active state.
func TestPrivateATSPIActiveOnlyCorrelationCannotGuessInactiveFrame(t *testing.T) {
	backend, _, target := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_ *syntheticATSPINode, frame *syntheticATSPINode) {
		frame.states = nil
	})
	target.PID = 0
	target.PIDHint = 0
	target.Title = ""
	target.Bounds = Rect{}
	target.AppID = "Editor"
	target.Active = true
	target.Focused = false
	for _, fresh := range []bool{false, true} {
		label := "cached"
		if fresh {
			label = "fresh"
		}
		t.Run(label, func(t *testing.T) {
			var scope WindowScope
			var err error
			if fresh {
				scope, err = backend.ResolveWindowFresh(context.Background(), target)
			} else {
				scope, err = backend.ResolveWindow(context.Background(), target)
			}
			if !errors.Is(err, ErrNotFound) || scope.Root.BusName != "" {
				t.Fatalf("active-only correlation guessed unobserved focus: scope=%+v err=%v", scope, err)
			}
		})
	}
}
