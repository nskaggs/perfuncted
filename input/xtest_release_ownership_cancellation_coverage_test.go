//go:build linux

package input

import (
	"context"
	"errors"
	"testing"

	"github.com/jezek/xgb/xproto"
)

func TestXTestKeyUpRejectsUnknownKeyBeforeWireDispatch(t *testing.T) {
	backend, events := newTestXTestBackend(t, 1, []xproto.Keysym{'a'}, nil)
	t.Cleanup(func() { _ = backend.Close() })

	if err := backend.KeyUp(context.Background(), "unmapped-key"); err == nil {
		t.Fatal("unknown KeyUp was accepted")
	}
	if len(*events) != 0 {
		t.Fatalf("unknown KeyUp reached XTEST: %+v", *events)
	}
}

func TestXTestKeyUpFailurePreservesHeldModifierOwnership(t *testing.T) {
	const ctrlKeycode = xproto.Keycode(8)
	releaseErr := errors.New("synthetic XTEST key-release failure")
	backend, events := newTestXTestBackend(t, 1, []xproto.Keysym{0xffe3}, func(event xtestEvent) error {
		if event.eventType == xproto.KeyRelease {
			return releaseErr
		}
		return nil
	})
	t.Cleanup(func() { _ = backend.Close() })
	backend.markModifierHeld(ctrlKeycode)

	if err := backend.KeyUp(context.Background(), "ctrl"); !errors.Is(err, releaseErr) {
		t.Fatalf("KeyUp error = %v, want release failure", err)
	}
	if !backend.modifierHeld(ctrlKeycode) {
		t.Fatal("failed KeyUp discarded caller-owned modifier state")
	}
	want := []xtestEvent{{eventType: xproto.KeyRelease, detail: byte(ctrlKeycode)}}
	if !sameXTestEvents(*events, want) {
		t.Fatalf("KeyUp wire events = %+v, want %+v", *events, want)
	}
}

func TestXTestClickCancellationAfterPressStillReleasesButton(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend, events := newTestXTestBackend(t, 1, []xproto.Keysym{'a'}, func(event xtestEvent) error {
		if event.eventType == xproto.ButtonPress {
			cancel()
		}
		return nil
	})
	t.Cleanup(func() { _ = backend.Close() })

	if err := backend.MouseClick(ctx, 12, 34, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("MouseClick error = %v, want context.Canceled", err)
	}
	want := []xtestEvent{
		{eventType: xproto.MotionNotify},
		{eventType: xproto.ButtonPress, detail: 1},
		{eventType: xproto.ButtonRelease, detail: 1},
	}
	if !sameXTestEvents(*events, want) {
		t.Fatalf("canceled click wire events = %+v, want balanced press/release %+v", *events, want)
	}
}
