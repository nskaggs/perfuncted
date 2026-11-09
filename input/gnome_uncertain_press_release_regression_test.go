//go:build linux

package input

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
)

// A bridge may report a transport error after delivering the button-down
// notification. In that uncertain state, a compensating release is required.
type uncertainGnomePressBridge struct {
	pointerGnomeBridge
	pressErr   error
	releaseErr error
	cancel     context.CancelFunc
}

func (b *uncertainGnomePressBridge) PointerButton(ctx context.Context, button uint32, pressed bool) error {
	if pressed {
		b.events = append(b.events, fmt.Sprintf("down:%d", button))
		if b.cancel != nil {
			b.cancel()
		}
		return b.pressErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.events = append(b.events, fmt.Sprintf("up:%d", button))
	return b.releaseErr
}

func TestGnomeClickCleansUpUncertainPressAndPreservesFailures(t *testing.T) {
	for _, tc := range []struct {
		name         string
		button       int
		cancel       bool
		releaseFails bool
	}{
		{name: "left press fails", button: 1},
		{name: "middle press and release fail", button: 2, releaseFails: true},
		{name: "right press fails and cancels operation", button: 3, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pressErr := errors.New("bridge partially delivered press")
			releaseErr := errors.New("bridge failed to release")
			bridge := &uncertainGnomePressBridge{pressErr: pressErr}
			if tc.cancel {
				bridge.cancel = cancel
			}
			if tc.releaseFails {
				bridge.releaseErr = releaseErr
			}
			b := &GnomeNativeBackend{bridge: bridge}
			err := b.MouseClick(ctx, 12, 34, tc.button)
			if !errors.Is(err, pressErr) || errors.Is(err, releaseErr) != tc.releaseFails || (tc.cancel && !errors.Is(err, context.Canceled)) {
				t.Fatalf("click failed to preserve press, release, cancellation errors: %v", err)
			}
			want := []string{"move:12,34", fmt.Sprintf("down:%d", tc.button), fmt.Sprintf("up:%d", tc.button)}
			if !slices.Equal(bridge.events, want) {
				t.Fatalf("bridge events=%v, want=%v", bridge.events, want)
			}
		})
	}
}
