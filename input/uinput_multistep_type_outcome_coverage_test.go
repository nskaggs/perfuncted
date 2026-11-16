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

func TestUinputTypeStopsAfterLiteralWriteFailure(t *testing.T) {
	failure := errors.New("synthetic literal key write failure")
	keyboard := &failingKeyboard{failPress: true, pressErr: failure}
	backend := &UinputBackend{kb: keyboard, charToRune: qwertyRuneMap()}

	err := backend.Type(context.Background(), "a{enter}")
	if !errors.Is(err, failure) {
		t.Fatalf("Type returned %v, want the literal write failure", err)
	}
	if len(keyboard.events) != 0 {
		t.Fatalf("Type sent later key actions after the failed literal write: %v", keyboard.events)
	}
}

func TestUinputTypeStopsAfterLiteralWriteCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	keyboard := &uinputCancelOnKeyboardPress{recordingKeyboard: &recordingKeyboard{}, cancel: cancel}
	backend := &UinputBackend{kb: keyboard, charToRune: qwertyRuneMap()}

	err := backend.Type(ctx, "a{enter}")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Type returned %v after cancellation, want context.Canceled", err)
	}
	want := []string{fmt.Sprintf("press:%d", uinput.KeyA)}
	if !slices.Equal(keyboard.events, want) {
		t.Fatalf("Type events after literal cancellation = %v, want %v", keyboard.events, want)
	}
}
