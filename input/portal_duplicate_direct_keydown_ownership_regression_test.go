//go:build linux

package input

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A second explicit KeyDown must not inject a repeat press for a key already
// owned by this EIS backend. This must be enforced on the serialized writer.
func TestRemoteDesktopRepeatedDirectKeyDownRetainsSingleOwnedPress(t *testing.T) {
	for _, key := range []string{"a", "enter", "ctrl", "f5"} {
		t.Run(key, func(t *testing.T) {
			assertDirectKeyDownOwnership(t, key)
		})
	}
}

func assertDirectKeyDownOwnership(t *testing.T, key string) {
	t.Helper()
	b, peer := newWireObservedRemoteDesktop(t)
	ctx := context.Background()
	if err := b.KeyDown(ctx, key); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := readSyntheticEISFrame(peer); err != nil {
			t.Fatal(err)
		}
	}
	code, err := remoteKeyCode(key)
	if err != nil {
		t.Fatal(err)
	}
	if !b.heldKeys[code] {
		t.Fatal("first down not recorded")
	}
	err = b.KeyDown(ctx, key)
	if err == nil || !strings.Contains(err.Error(), "already held") {
		t.Fatalf("duplicate key down accepted: %v", err)
	}
	assertNoEIWireRequest(t, peer)
	if !b.heldKeys[code] || len(b.heldKeys) != 1 {
		t.Fatalf("denied duplicate changed ownership: %v", b.heldKeys)
	}
	if err := b.KeyUp(ctx, key); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := readSyntheticEISFrame(peer); err != nil {
			t.Fatal(err)
		}
	}
	if len(b.heldKeys) != 0 {
		t.Fatalf("release after denied duplicate failed: %v", b.heldKeys)
	}
	if err := b.KeyDown(ctx, key); err != nil {
		t.Fatalf("legitimate repress failed: %v", err)
	}
}

func TestRemoteDesktopDirectKeyDownReportsMissingKeyboardAuthorityWithoutWireEffects(t *testing.T) {
	for _, disable := range []string{"keyboard interface", "keyboard device"} {
		t.Run(disable, func(t *testing.T) {
			b, peer := newWireObservedRemoteDesktop(t)
			if disable == "keyboard interface" {
				b.sender.keyboard = 0
			} else {
				b.sender.keyboardDev = 0
			}
			err := b.KeyDown(context.Background(), "a")
			if !errors.Is(err, ErrNotSupported) {
				t.Fatalf("unavailable keyboard did not reject with capability error: %v", err)
			}
			assertNoEIWireRequest(t, peer)
			if len(b.heldKeys) != 0 {
				t.Fatalf("unavailable keyboard claimed held keys: %+v", b.heldKeys)
			}
		})
	}
}
