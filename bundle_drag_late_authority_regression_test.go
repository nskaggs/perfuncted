package perfuncted_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
)

type revokeLateDragInput struct {
	pftest.Inputter
	after    func()
	on       string
	moves    int
	releases int
}

func (b *revokeLateDragInput) MouseMove(ctx context.Context, x, y int) error {
	b.moves++
	err := b.Inputter.MouseMove(ctx, x, y)
	if b.on == "destination" && b.moves == 2 {
		b.after()
	}
	return err
}
func (b *revokeLateDragInput) MouseUp(ctx context.Context, button int) error {
	b.releases++
	err := b.Inputter.MouseUp(ctx, button)
	if b.on == "release" && b.releases == 1 {
		b.after()
	}
	return err
}

// Once the mouse button is pressed, a late revoked destination move still
// needs a best-effort release, but cannot be reported as a successful drag.
// Revocation during the final release likewise cannot certify success.
func TestDragReportsAuthorityLossDuringHeldGesture(t *testing.T) {
	for _, phase := range []string{"destination", "release"} {
		for _, mode := range []string{"cancel", "close"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				backend := &revokeLateDragInput{on: phase}
				session := perfuncted.NewSessionForTesting(nil, backend, nil, nil, nil)
				t.Cleanup(func() { _ = session.Close() })
				expected := error(context.Canceled)
				if mode == "cancel" {
					backend.after = cancel
				} else {
					expected = perfuncted.ErrSessionClosed
					backend.after = func() { _ = session.Close() }
				}
				err := session.Input.DragAndDrop(ctx, 11, 22, 33, 44)
				want := []string{"move:11,22", "mousedown", "move:33,44", "mouseup"}
				if !errors.Is(err, expected) || !reflect.DeepEqual(backend.Calls, want) {
					t.Fatalf("revoked drag: err=%v calls=%v expected error %v, calls %v", err, backend.Calls, expected, want)
				}
			})
		}
	}
}
