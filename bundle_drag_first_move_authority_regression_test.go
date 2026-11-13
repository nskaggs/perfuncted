package perfuncted_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
)

type revokeAfterDragMoveInput struct {
	pftest.Inputter
	after     func()
	moveCalls int
}

func (b *revokeAfterDragMoveInput) MouseMove(ctx context.Context, x, y int) error {
	b.moveCalls++
	err := b.Inputter.MouseMove(ctx, x, y)
	if b.moveCalls == 1 && b.after != nil {
		b.after()
	}
	return err
}

// A successful first pointer move can complete after its request was revoked.
// Do not start a button hold or issue a release for a button never pressed.
func TestDragDoesNotPressAfterFirstPointerMoveLosesAuthority(t *testing.T) {
	for _, mode := range []string{"cancel", "close"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backend := &revokeAfterDragMoveInput{}
			session := perfuncted.NewSessionForTesting(nil, backend, nil, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			wantErr := error(context.Canceled)
			if mode == "cancel" {
				backend.after = cancel
			} else {
				wantErr = perfuncted.ErrSessionClosed
				backend.after = func() { _ = session.Close() }
			}
			err := session.Input.DragAndDrop(ctx, 10, 20, 30, 40)
			wantCalls := []string{"move:10,20"}
			if !errors.Is(err, wantErr) || !reflect.DeepEqual(backend.Calls, wantCalls) {
				t.Fatalf("drag after authority loss: err=%v calls=%v, want error %v and calls %v", err, backend.Calls, wantErr, wantCalls)
			}
		})
	}
}
