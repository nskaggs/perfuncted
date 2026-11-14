package input

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/keymap"
)

type cancelAfterGNOMEActionBridge struct {
	recordingGnomeBridge
	cancel      context.CancelFunc
	cancelOnKey uint32
	textCalls   int
}

func (b *cancelAfterGNOMEActionBridge) Key(ctx context.Context, keyval uint32, pressed bool) error {
	err := b.recordingGnomeBridge.Key(ctx, keyval, pressed)
	if pressed && keyval == b.cancelOnKey {
		b.cancel()
	}
	return err
}
func (b *cancelAfterGNOMEActionBridge) Text(_ context.Context, _ string) error {
	b.textCalls++
	b.cancel()
	return nil
}

// A bridge may complete a successful first event just as the caller cancels.
// A typed sequence must not dispatch any subsequent private input and must
// release only the modifier the canceled operation acquired.
func TestGnomeTypeStopsAfterCancellationBetweenActions(t *testing.T) {
	ctrl := gnomeSpecialKeyvals[keymap.KeyCtrl]
	for _, tc := range []struct {
		name, sequence string
		cancelOnKey    uint32
		wantKeyDown    int
		wantTextCalls  int
	}{
		{"after modifier press", "{ctrl down}{ctrl+a}", ctrl, 1, 0},
		{"after literal text", "prefix{enter}", 0, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			bridge := &cancelAfterGNOMEActionBridge{cancel: cancel, cancelOnKey: tc.cancelOnKey}
			backend := &GnomeNativeBackend{bridge: bridge}
			err := backend.Type(ctx, tc.sequence)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled sequence returned %v", err)
			}
			if bridge.textCalls != tc.wantTextCalls {
				t.Fatalf("unexpected text commands=%d", bridge.textCalls)
			}
			if countGnomeKeyEvents(bridge.events, "down", ctrl) != tc.wantKeyDown {
				t.Fatalf("unexpected modifier press: %v", bridge.events)
			}
			if countGnomeKeyEvents(bridge.events, "up", ctrl) != tc.wantKeyDown {
				t.Fatalf("caller cancellation did not clean up acquired modifier: %v", bridge.events)
			}
			if countGnomeKeyEvents(bridge.events, "down", uint32('a')) != 0 || countGnomeKeyEvents(bridge.events, "down", 0xff0d) != 0 {
				t.Fatalf("subsequent private keys dispatched after cancel: %v", bridge.events)
			}
			if backend.held.any() {
				t.Fatalf("canceled sequence retained phantom modifiers: %+v", backend.held)
			}
		})
	}
}
