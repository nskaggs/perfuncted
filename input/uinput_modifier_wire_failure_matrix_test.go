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

type syntheticFaultKeyboard struct {
	*recordingKeyboard
	failAt  int
	calls   int
	failure error
}

func (k *syntheticFaultKeyboard) fail() error {
	k.calls++
	if k.calls == k.failAt {
		return k.failure
	}
	return nil
}
func (k *syntheticFaultKeyboard) KeyDown(code int) error {
	k.events = append(k.events, fmt.Sprintf("down:%d", code))
	return k.fail()
}
func (k *syntheticFaultKeyboard) KeyUp(code int) error {
	k.events = append(k.events, fmt.Sprintf("up:%d", code))
	return k.fail()
}
func (k *syntheticFaultKeyboard) KeyPress(code int) error {
	k.events = append(k.events, fmt.Sprintf("press:%d", code))
	return k.fail()
}

var _ uinput.Keyboard = (*syntheticFaultKeyboard)(nil)

func TestUinputTemporaryModifierFailureRollsBackRealWireSequence(t *testing.T) {
	failure := errors.New("synthetic evdev device failure")
	ctrl, a := uinput.KeyLeftctrl, uinput.KeyA
	wantSuccess := []string{fmt.Sprintf("down:%d", ctrl), fmt.Sprintf("press:%d", a), fmt.Sprintf("up:%d", ctrl)}
	for _, tc := range []struct {
		failAt int
		want   []string
	}{
		{1, wantSuccess[:1]},
		{2, wantSuccess},
		{3, wantSuccess},
		{0, wantSuccess},
	} {
		t.Run(fmt.Sprintf("fail_at_%d", tc.failAt), func(t *testing.T) {
			kb := &syntheticFaultKeyboard{recordingKeyboard: &recordingKeyboard{}, failAt: tc.failAt, failure: failure}
			b := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}
			err := b.Type(context.Background(), "{ctrl+a}")
			if (tc.failAt != 0) != errors.Is(err, failure) {
				t.Fatalf("evdev failure at %d reported %v", tc.failAt, err)
			}
			if !slices.Equal(kb.events, tc.want) {
				t.Fatalf("injected failure at %d events=%v, want %v", tc.failAt, kb.events, tc.want)
			}
			if b.modifierHeld(ctrl) {
				t.Fatal("temporary modifier leaked into caller-owned hold state")
			}
		})
	}
}

func TestUinputLiteralShiftFailurePreservesErrorAndAttemptsRelease(t *testing.T) {
	failure := errors.New("synthetic evdev device failure")
	for _, tc := range []struct {
		failAt int
		want   []string
	}{
		{1, []string{fmt.Sprintf("down:%d", uinput.KeyLeftshift)}},
		{2, []string{fmt.Sprintf("down:%d", uinput.KeyLeftshift), fmt.Sprintf("press:%d", uinput.KeyA), fmt.Sprintf("up:%d", uinput.KeyLeftshift)}},
		{3, []string{fmt.Sprintf("down:%d", uinput.KeyLeftshift), fmt.Sprintf("press:%d", uinput.KeyA), fmt.Sprintf("up:%d", uinput.KeyLeftshift)}},
		{0, []string{fmt.Sprintf("down:%d", uinput.KeyLeftshift), fmt.Sprintf("press:%d", uinput.KeyA), fmt.Sprintf("up:%d", uinput.KeyLeftshift)}},
	} {
		t.Run(fmt.Sprintf("fail_at_%d", tc.failAt), func(t *testing.T) {
			kb := &syntheticFaultKeyboard{recordingKeyboard: &recordingKeyboard{}, failAt: tc.failAt, failure: failure}
			b := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}
			err := b.TypeLiteral(context.Background(), "A")
			if (tc.failAt != 0) != errors.Is(err, failure) {
				t.Fatalf("evdev failure at %d reported %v", tc.failAt, err)
			}
			if !slices.Equal(kb.events, tc.want) {
				t.Fatalf("injected failure at %d events=%v want %v", tc.failAt, kb.events, tc.want)
			}
			if b.modifierHeld(uinput.KeyLeftshift) {
				t.Fatal("temporary shift leaked into caller-held state")
			}
		})
	}
}
