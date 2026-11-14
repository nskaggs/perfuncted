package input

import (
	"context"
	"errors"
	"testing"
)

// A canceled composed key request must stop before consulting the keyboard
// device or attempting to press temporary modifiers.
func TestUinputComposedKeyDispatchRejectsCanceledRequestBeforeDeviceAccess(t *testing.T) {
	var backend UinputBackend
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := backend.typeKeyWithMods(ctx, 1, false, false, modifiers{shift: true, ctrl: true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled key request reached device dispatch: %v", err)
	}
}
