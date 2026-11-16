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

func TestUinputTypeExplicitKeyWriteFailuresPreserveOwnership(t *testing.T) {
	t.Run("failed key-down does not create a hold", func(t *testing.T) {
		failure := errors.New("synthetic key-down failure")
		keyboard := &failDirectKeyDown{recordingKeyboard: &recordingKeyboard{}, downErr: failure}
		backend := &UinputBackend{kb: keyboard, charToRune: qwertyRuneMap()}

		err := backend.Type(context.Background(), "{enter down}")
		if !errors.Is(err, failure) {
			t.Fatalf("Type returned %v, want key-down failure", err)
		}
		if backend.keyHeld(uinput.KeyEnter) || len(keyboard.events) != 0 {
			t.Fatalf("failed key-down created ownership or events: held=%v events=%v", backend.keyHeld(uinput.KeyEnter), keyboard.events)
		}
	})

	t.Run("failed key-up preserves the hold", func(t *testing.T) {
		failure := errors.New("synthetic key-up failure")
		keyboard := &failingKeyboard{failUp: true, failUpKey: uinput.KeyEnter, upErr: failure}
		backend := &UinputBackend{
			kb:         keyboard,
			charToRune: qwertyRuneMap(),
			heldKeys:   map[int]bool{uinput.KeyEnter: true},
		}

		err := backend.Type(context.Background(), "{enter up}")
		if !errors.Is(err, failure) {
			t.Fatalf("Type returned %v, want key-up failure", err)
		}
		want := []string{fmt.Sprintf("up:%d", uinput.KeyEnter)}
		if !backend.keyHeld(uinput.KeyEnter) || !slices.Equal(keyboard.events, want) {
			t.Fatalf("failed key-up changed ownership or emitted unexpected events: held=%v events=%v", backend.keyHeld(uinput.KeyEnter), keyboard.events)
		}
	})
}

func TestUinputDirectKeyOperationsRejectUnknownNamesBeforeEvents(t *testing.T) {
	backend, keyboard := newTestBackend(t)
	for _, operation := range []struct {
		name string
		call func() error
	}{
		{"KeyDown", func() error { return backend.KeyDown(context.Background(), "not-a-real-key") }},
		{"KeyUp", func() error { return backend.KeyUp(context.Background(), "not-a-real-key") }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.call(); err == nil {
				t.Fatal("unknown physical key name was accepted")
			}
			if len(keyboard.events) != 0 || len(backend.heldKeys) != 0 {
				t.Fatalf("invalid key changed backend state: events=%v held=%v", keyboard.events, backend.heldKeys)
			}
		})
	}
}
