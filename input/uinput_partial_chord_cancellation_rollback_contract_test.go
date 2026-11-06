//go:build linux

package input

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/bendahl/uinput"
)

type cancelAfterModifierDownKeyboard struct {
	*recordingKeyboard
	cancel         context.CancelFunc
	cancelCode     int
	releaseFailure error
}

func (k *cancelAfterModifierDownKeyboard) KeyDown(code int) error {
	err := k.recordingKeyboard.KeyDown(code)
	if code == k.cancelCode {
		k.cancel()
	}
	return err
}
func (k *cancelAfterModifierDownKeyboard) KeyUp(code int) error {
	err := k.recordingKeyboard.KeyUp(code)
	if k.releaseFailure != nil && code == uinput.KeyLeftctrl {
		return errors.Join(err, k.releaseFailure)
	}
	return err
}

var _ uinput.Keyboard = (*cancelAfterModifierDownKeyboard)(nil)

// In a multi-modifier chord, caller cancellation can occur immediately after
// one or more modifier-down writes. Any temporary hold already sent must be
// released in reverse order; the target key must never be injected.
func TestUinputCanceledPartialChordReleasesTemporaryModifiers(t *testing.T) {
	for _, tc := range []struct {
		name        string
		cancelCode  int
		want        []string
		failRelease bool
	}{
		{"cancel after first modifier", uinput.KeyLeftctrl, []string{
			fmt.Sprintf("down:%d", uinput.KeyLeftshift), fmt.Sprintf("down:%d", uinput.KeyLeftctrl), fmt.Sprintf("up:%d", uinput.KeyLeftctrl), fmt.Sprintf("up:%d", uinput.KeyLeftshift),
		}, false},
		{"cancel after final modifier", uinput.KeyLeftalt, []string{
			fmt.Sprintf("down:%d", uinput.KeyLeftshift), fmt.Sprintf("down:%d", uinput.KeyLeftctrl), fmt.Sprintf("down:%d", uinput.KeyLeftalt), fmt.Sprintf("up:%d", uinput.KeyLeftalt), fmt.Sprintf("up:%d", uinput.KeyLeftctrl), fmt.Sprintf("up:%d", uinput.KeyLeftshift),
		}, false},
		{"rollback release also fails", uinput.KeyLeftctrl, []string{
			fmt.Sprintf("down:%d", uinput.KeyLeftshift), fmt.Sprintf("down:%d", uinput.KeyLeftctrl), fmt.Sprintf("up:%d", uinput.KeyLeftctrl), fmt.Sprintf("up:%d", uinput.KeyLeftshift),
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			releaseFailure := errors.New("synthetic modifier release error")
			kb := &cancelAfterModifierDownKeyboard{recordingKeyboard: &recordingKeyboard{}, cancel: cancel, cancelCode: tc.cancelCode}
			if tc.failRelease {
				kb.releaseFailure = releaseFailure
			}
			backend := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}
			err := backend.Type(ctx, "{shift+ctrl+alt+a}")
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled chord returned %v", err)
			}
			if errors.Is(err, releaseFailure) != tc.failRelease {
				t.Fatalf("cleanup error not preserved correctly: %v", err)
			}
			if !slices.Equal(kb.events, tc.want) {
				t.Fatalf("incomplete chord events=%v, want %v", kb.events, tc.want)
			}
			for _, code := range []int{uinput.KeyLeftshift, uinput.KeyLeftctrl, uinput.KeyLeftalt} {
				if backend.modifierHeld(code) {
					t.Fatalf("temporary modifier %d became caller-owned", code)
				}
			}
		})
	}
}

// TypeLiteral verifies each rune's lifetime after the previous keyboard write:
// a device succeeding on character one cannot authorize character two after
// the caller cancels, even when the first output was valid.
func TestUinputLiteralCancellationCannotTypeFollowingCharacters(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	kb := &uinputCancelOnKeyboardPress{recordingKeyboard: &recordingKeyboard{}, cancel: cancel}
	backend := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}
	if err := backend.TypeLiteral(ctx, "ab"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled literal reported %v", err)
	}
	want := []string{fmt.Sprintf("press:%d", uinput.KeyA)}
	if !slices.Equal(kb.events, want) {
		t.Fatalf("typed after revocation: %v", kb.events)
	}
}
