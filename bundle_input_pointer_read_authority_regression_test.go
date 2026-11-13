package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/input"
)

type authorityLossPointerRead struct {
	*closeCountingInput
	after         func()
	locationCalls int
	spaceCalls    int
}

func (b *authorityLossPointerRead) PointerLocation(context.Context) (int, int, error) {
	b.locationCalls++
	if b.after != nil {
		b.after()
	}
	return 314, 159, nil
}
func (b *authorityLossPointerRead) PointerCoordinateSpace(context.Context) (input.CoordinateSpaceInfo, error) {
	b.spaceCalls++
	if b.after != nil {
		b.after()
	}
	return input.CoordinateSpaceInfo{Kind: input.CoordinateSpacePhysical, ScaleX: 2, ScaleY: 1.5}, nil
}

// A provider may acknowledge private pointer metadata after authorization ends.
// The facade must refuse both the late result and pre-canceled dispatch.
func TestInputPointerReadsRequireAuthorityAtDispatchAndReturn(t *testing.T) {
	for _, operation := range []string{"position", "coordinate-space"} {
		for _, mode := range []string{"pre-cancel", "late-cancel", "close"} {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				source := &authorityLossPointerRead{closeCountingInput: &closeCountingInput{}}
				session := NewSessionForTesting(nil, source, nil, nil, nil)
				t.Cleanup(func() { _ = session.Close() })
				expected := error(context.Canceled)
				if mode == "pre-cancel" {
					cancel()
				} else if mode == "late-cancel" {
					source.after = cancel
				} else {
					expected = ErrSessionClosed
					source.after = func() { _ = session.Close() }
				}
				var err error
				if operation == "position" {
					x, y, e := session.Input.PointerLocation(ctx)
					err = e
					if x != 0 || y != 0 {
						t.Errorf("leaked position %d,%d", x, y)
					}
				} else {
					info, e := session.Input.PointerCoordinateSpace(ctx)
					err = e
					if info != (input.CoordinateSpaceInfo{}) {
						t.Errorf("leaked coordinate space %+v", info)
					}
				}
				calls := source.locationCalls + source.spaceCalls
				wantCalls := 1
				if mode == "pre-cancel" {
					wantCalls = 0
				}
				if !errors.Is(err, expected) || calls != wantCalls {
					t.Errorf("err=%v, calls=%d want err %v calls %d", err, calls, expected, wantCalls)
				}
			})
		}
	}
}
