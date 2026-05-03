//go:build linux
// +build linux

package input

import (
	"context"
	"fmt"
	"testing"

	"github.com/bendahl/uinput"
)

type recordingKeyboard struct {
	events []string
}

func (k *recordingKeyboard) KeyPress(key int) error {
	k.events = append(k.events, fmt.Sprintf("press:%d", key))
	return nil
}

func (k *recordingKeyboard) KeyDown(key int) error {
	k.events = append(k.events, fmt.Sprintf("down:%d", key))
	return nil
}

func (k *recordingKeyboard) KeyUp(key int) error {
	k.events = append(k.events, fmt.Sprintf("up:%d", key))
	return nil
}

func (k *recordingKeyboard) FetchSyspath() (string, error) { return "", nil }

func (k *recordingKeyboard) Close() error { return nil }

var _ uinput.Keyboard = (*recordingKeyboard)(nil)

func TestUinputTypeContextUppercaseUsesShift(t *testing.T) {
	kb := &recordingKeyboard{}
	b := &UinputBackend{kb: kb}

	if err := b.TypeContext(context.Background(), "A"); err != nil {
		t.Fatalf("TypeContext: %v", err)
	}

	want := []string{
		fmt.Sprintf("down:%d", uinput.KeyLeftshift),
		fmt.Sprintf("press:%d", uinput.KeyA),
		fmt.Sprintf("up:%d", uinput.KeyLeftshift),
	}
	if len(kb.events) != len(want) {
		t.Fatalf("unexpected events: got %v want %v", kb.events, want)
	}
	for i, event := range want {
		if kb.events[i] != event {
			t.Fatalf("event %d = %q, want %q (all events: %v)", i, kb.events[i], event, kb.events)
		}
	}
}

func TestUinputTypeContextLowercaseDoesNotUseShift(t *testing.T) {
	kb := &recordingKeyboard{}
	b := &UinputBackend{kb: kb}

	if err := b.TypeContext(context.Background(), "a"); err != nil {
		t.Fatalf("TypeContext: %v", err)
	}

	want := []string{fmt.Sprintf("press:%d", uinput.KeyA)}
	if len(kb.events) != len(want) {
		t.Fatalf("unexpected events: got %v want %v", kb.events, want)
	}
	for i, event := range want {
		if kb.events[i] != event {
			t.Fatalf("event %d = %q, want %q (all events: %v)", i, kb.events[i], event, kb.events)
		}
	}
}
