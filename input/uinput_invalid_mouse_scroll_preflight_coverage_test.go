package input

import (
	"context"
	"strings"
	"testing"
)

// Malformed wheel requests must be rejected before the backend tries to
// allocate its lazy mouse device, even without an attached uinput transport.
func TestUinputPublicScrollDirectionsRejectInvalidCountsBeforeDeviceAccess(t *testing.T) {
	var backend UinputBackend
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		call func(int) error
	}{
		{"up", func(n int) error { return backend.ScrollUp(ctx, n) }},
		{"down", func(n int) error { return backend.ScrollDown(ctx, n) }},
		{"left", func(n int) error { return backend.ScrollLeft(ctx, n) }},
		{"right", func(n int) error { return backend.ScrollRight(ctx, n) }},
	} {
		for _, n := range []int{-1, maxScrollClicks + 1} {
			t.Run(tc.name+"/invalid", func(t *testing.T) {
				err := tc.call(n)
				if err == nil || !strings.Contains(err.Error(), "scroll clicks") {
					t.Fatalf("invalid count %d was dispatched: %v", n, err)
				}
			})
		}
	}
}

// Internal helpers are also required to validate button IDs independently
// of public API preflight. An unavailable touchpad must not mask the error.
func TestUinputInternalMouseButtonsRejectInvalidIDsBeforeDeviceAccess(t *testing.T) {
	var backend UinputBackend
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		call func(int) error
	}{
		{"down", func(n int) error { return backend.mouseDown(ctx, n) }},
		{"up", func(n int) error { return backend.mouseUp(ctx, n) }},
	} {
		for _, id := range []int{-1, 4} {
			t.Run(tc.name+"/invalid", func(t *testing.T) {
				err := tc.call(id)
				if err == nil || !strings.Contains(err.Error(), "button") {
					t.Fatalf("invalid mouse button %d was dispatched: %v", id, err)
				}
			})
		}
	}
}
