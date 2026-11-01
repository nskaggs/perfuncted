//go:build linux

package input

import (
	"context"
	"errors"
	"testing"
)

// A late GNOME D-Bus reply after cancellation must not leak a point alongside
// its cancellation error. Callers can accidentally use nonzero output values
// even when they correctly check err in an upstream adapter.
func TestGnomeCanceledPointerLocationDoesNotPublishLateCoordinates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bridge := &cancelingGnomeBridge{cancel: cancel}
	b := &GnomeNativeBackend{bridge: bridge}
	x, y, err := b.PointerLocation(ctx)
	if !errors.Is(err, context.Canceled) || x != 0 || y != 0 {
		t.Fatalf("late canceled pointer result disclosed (%d,%d) error=%v", x, y, err)
	}
}

type gnomeErrorPointerBridge struct{ lifecycleGnomeBridge }

func (b *gnomeErrorPointerBridge) PointerLocation(context.Context) (int, int, error) {
	return 117, 302, errors.New("synthetic pointer provider failed")
}
func TestGnomeFailedPointerLocationDoesNotPublishUntrustedCoordinates(t *testing.T) {
	b := &GnomeNativeBackend{bridge: &gnomeErrorPointerBridge{}}
	x, y, err := b.PointerLocation(context.Background())
	if err == nil || x != 0 || y != 0 {
		t.Fatalf("failed provider result disclosed (%d,%d) error=%v", x, y, err)
	}
}
