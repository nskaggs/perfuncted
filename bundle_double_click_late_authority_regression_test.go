package perfuncted_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
)

type revokeDuringDoubleClickInput struct {
	pftest.Inputter
	on       string
	after    func()
	releases int
}

func (b *revokeDuringDoubleClickInput) MouseMove(ctx context.Context, x, y int) error {
	err := b.Inputter.MouseMove(ctx, x, y)
	if b.on == "initial-move" {
		b.after()
	}
	return err
}
func (b *revokeDuringDoubleClickInput) MouseUp(ctx context.Context, button int) error {
	b.releases++
	err := b.Inputter.MouseUp(ctx, button)
	if b.on == "final-release" && b.releases == 2 {
		b.after()
	}
	return err
}

// A non-cooperative provider cannot turn a revoked gesture into success.
// Early revocation must not start a button press; late revocation must not
// acknowledge a successful double-click after its final release.
func TestDoubleClickRejectsAuthorityLossAtGestureBoundaries(t *testing.T) {
	for _, phase := range []string{"initial-move", "final-release"} {
		for _, mode := range []string{"cancel", "close"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				backend := &revokeDuringDoubleClickInput{on: phase}
				session := perfuncted.NewSessionForTesting(nil, backend, nil, nil, nil)
				t.Cleanup(func() { _ = session.Close() })
				wantErr := error(context.Canceled)
				if mode == "cancel" {
					backend.after = cancel
				} else {
					wantErr = perfuncted.ErrSessionClosed
					backend.after = func() { _ = session.Close() }
				}
				err := session.Input.DoubleClick(ctx, 14, 23)
				want := []string{"move:14,23"}
				if phase == "final-release" {
					want = append(want, "mousedown", "mouseup", "mousedown", "mouseup")
				}
				if !errors.Is(err, wantErr) || !reflect.DeepEqual(backend.Calls, want) {
					t.Fatalf("double-click after %s/%s: err=%v calls=%v, want error=%v calls=%v", phase, mode, err, backend.Calls, wantErr, want)
				}
			})
		}
	}
}
