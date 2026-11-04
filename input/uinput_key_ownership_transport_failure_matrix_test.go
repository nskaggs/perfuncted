//go:build linux

package input

import (
	"context"
	"errors"
	"testing"

	"github.com/bendahl/uinput"
)

type failDirectKeyDown struct {
	*recordingKeyboard
	downErr error
}

func (f *failDirectKeyDown) KeyDown(key int) error {
	if f.downErr != nil {
		return f.downErr
	}
	return f.recordingKeyboard.KeyDown(key)
}

var _ uinput.Keyboard = (*failDirectKeyDown)(nil)

// Failed device writes must not make the backend claim ownership of a key it
// never pressed or relinquish one for which release was not confirmed.
func TestUinputDirectKeyFailuresPreserveCallerOwnedState(t *testing.T) {
	ctx := context.Background()
	t.Run("failed press is not held", func(t *testing.T) {
		failure := errors.New("synthetic keyboard press failure")
		device := &failDirectKeyDown{recordingKeyboard: &recordingKeyboard{}, downErr: failure}
		b := &UinputBackend{kb: device, charToRune: qwertyRuneMap()}
		if err := b.KeyDown(ctx, "ctrl"); !errors.Is(err, failure) {
			t.Fatalf("failed keyboard press returned %v", err)
		}
		if b.keyHeld(uinput.KeyLeftctrl) || len(device.events) != 0 {
			t.Fatalf("failed device write created keyboard ownership: %+v", device.events)
		}
		device.downErr = nil
		if err := b.KeyDown(ctx, "ctrl"); err != nil {
			t.Fatalf("retry press: %v", err)
		}
		if !b.keyHeld(uinput.KeyLeftctrl) {
			t.Fatal("successful retry did not establish hold")
		}
		if err := b.KeyUp(ctx, "ctrl"); err != nil {
			t.Fatalf("release: %v", err)
		}
	})
	t.Run("failed release remains owned", func(t *testing.T) {
		failure := errors.New("synthetic keyboard release failure")
		device := &failingKeyboard{failUp: true, failUpKey: uinput.KeyA, upErr: failure}
		b := &UinputBackend{kb: device, charToRune: qwertyRuneMap()}
		if err := b.KeyDown(ctx, "a"); err != nil {
			t.Fatalf("press: %v", err)
		}
		if err := b.KeyUp(ctx, "a"); !errors.Is(err, failure) {
			t.Fatalf("failed keyboard release returned %v", err)
		}
		if !b.keyHeld(uinput.KeyA) {
			t.Fatal("failed release dropped caller's key hold")
		}
		if err := b.KeyDown(ctx, "a"); err == nil {
			t.Fatal("failed release permitted duplicate key-down")
		}
		device.failUp = false
		if err := b.KeyUp(ctx, "a"); err != nil {
			t.Fatalf("retry release: %v", err)
		}
		if b.keyHeld(uinput.KeyA) {
			t.Fatal("successful release still held")
		}
	})
}
