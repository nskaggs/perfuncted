//go:build linux

package input

import (
	"context"
	"errors"
	"testing"
)

// A canceled virtual keyboard request must not inspect held state or reject
// malformed text before acknowledging cancellation, even via direct helpers.
func TestVirtualKeyboardDirectKeyOperationsRejectCanceledCallerBeforeValidation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name   string
		invoke func(*wlKeyboard) error
	}{
		{"press invalid multi-character", func(k *wlKeyboard) error { return k.pressKey(ctx, "not_a_single_key") }},
		{"release invalid multi-character", func(k *wlKeyboard) error { return k.releaseKey(ctx, "not_a_single_key") }},
		{"release unheld modifier", func(k *wlKeyboard) error { return k.releaseKey(ctx, "ctrl") }},
		{"release unheld literal", func(k *wlKeyboard) error { return k.releaseKey(ctx, "x") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, wire := newTestKeyboard()
			err := tc.invoke(k)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled operation returned %v", err)
			}
			if len(wire.msgs) != 0 || len(k.held) != 0 || k.mods != 0 {
				t.Fatalf("canceled key request changed state or emitted messages")
			}
		})
	}
}
