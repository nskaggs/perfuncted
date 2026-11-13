package perfuncted_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
)

type revokeDuringDragValidation struct {
	pftest.Inputter
	checked int
	revoke  func()
}

func (b *revokeDuringDragValidation) ValidatePointerTarget(context.Context, int, int) error {
	b.checked++
	if b.checked == 2 && b.revoke != nil {
		b.revoke()
	}
	return nil
}

// Destination validation is the last non-mutating stage of a drag. Losing
// authority there must prevent *all* synthetic input from being injected.
func TestDragRejectsAuthorizationLossAfterDestinationValidation(t *testing.T) {
	for _, mode := range []string{"cancel", "close"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backend := &revokeDuringDragValidation{}
			session := perfuncted.NewSessionForTesting(nil, backend, nil, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			expected := error(context.Canceled)
			if mode == "cancel" {
				backend.revoke = cancel
			} else {
				expected = perfuncted.ErrSessionClosed
				backend.revoke = func() { _ = session.Close() }
			}
			err := session.Input.DragAndDrop(ctx, 20, 20, 40, 40)
			if !errors.Is(err, expected) || backend.checked != 2 || len(backend.Calls) != 0 {
				t.Fatalf("drag after %s: err=%v validations=%d injected=%v", mode, err, backend.checked, backend.Calls)
			}
		})
	}
}
