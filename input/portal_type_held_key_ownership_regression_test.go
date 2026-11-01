//go:build linux

package input

import (
	"context"
	"strings"
	"testing"
)

// A Type tap ends with key-up. If another operation is holding that key,
// accepting a tap would silently release the caller's drag/shortcut modifier.
// Reject the *entire* Type batch before any earlier text or key event escapes.
func TestRemoteDesktopTypeNeverTapsCallerHeldKey(t *testing.T) {
	for _, tc := range []struct{ name, key, batch string }{
		{"letter", "a", "{a}"},
		{"enter with prefix", "enter", "private{enter}"},
		{"modifier", "ctrl", "{ctrl}"},
		{"modified key", "a", "{ctrl+a}"},
		{"late tap after named key", "a", "{enter}{a}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, peer := newWireObservedRemoteDesktop(t)
			if err := b.KeyDown(context.Background(), tc.key); err != nil {
				t.Fatalf("establish held key: %v", err)
			}
			for i := 0; i < 2; i++ {
				if _, err := readSyntheticEISFrame(peer); err != nil {
					t.Fatalf("held key frame: %v", err)
				}
			}
			code, err := remoteKeyCode(tc.key)
			if err != nil {
				t.Fatal(err)
			}
			if !b.heldKeys[code] {
				t.Fatal("held key not recorded")
			}
			err = b.Type(context.Background(), tc.batch)
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("held key tapped/released by Type: %v", err)
			}
			assertNoEIWireRequest(t, peer)
			if !b.heldKeys[code] || len(b.heldKeys) != 1 {
				t.Fatalf("denied batch changed held input: %+v", b.heldKeys)
			}
			if err := b.KeyUp(context.Background(), tc.key); err != nil {
				t.Fatalf("caller cannot release held key: %v", err)
			}
			for i := 0; i < 2; i++ {
				if _, err := readSyntheticEISFrame(peer); err != nil {
					t.Fatalf("legitimate release not sent: %v", err)
				}
			}
			if len(b.heldKeys) != 0 {
				t.Fatalf("legitimate release did not clear state: %+v", b.heldKeys)
			}
		})
	}
}

// Preflight must simulate ownership within the batch as well as across calls.
// The later invalid tap must not leave an earlier down injected or recorded.
func TestRemoteDesktopTypePreflightRejectsTapAfterDownWithinSameBatch(t *testing.T) {
	for _, sequence := range []string{
		"{a down}{a}",
		"private{enter down}{enter}",
		"{ctrl down}{ctrl+a}{ctrl}",
	} {
		t.Run(sequence, func(t *testing.T) {
			b, peer := newWireObservedRemoteDesktop(t)
			err := b.Type(context.Background(), sequence)
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("invalid self-releasing batch accepted: %v", err)
			}
			assertNoEIWireRequest(t, peer)
			if len(b.heldKeys) != 0 {
				t.Fatalf("batch injected held keys despite rejection: %+v", b.heldKeys)
			}
			if stateErr := b.stateError(); stateErr != nil {
				t.Fatalf("batch rejected by revoking EIS authorization: %v", stateErr)
			}
		})
	}
}

// Balanced explicit ownership and independent key presses are still allowed.
func TestRemoteDesktopTypeBalancedSameKeyTransactionsStillWork(t *testing.T) {
	for _, sequence := range []string{"{a down}{a up}{a}", "{ctrl down}{ctrl+a}{ctrl up}", "{enter}{enter}"} {
		t.Run(sequence, func(t *testing.T) {
			b, peer := newWireObservedRemoteDesktop(t)
			if err := b.Type(context.Background(), sequence); err != nil {
				t.Fatalf("balanced EIS sequence rejected: %v", err)
			}
			if len(b.heldKeys) != 0 {
				t.Fatalf("balanced sequence left key held: %+v", b.heldKeys)
			}
			if frame, err := readSyntheticEISFrame(peer); err != nil || frame.object != b.sender.keyboard {
				t.Fatalf("normal key transitions absent: %+v err=%v", frame, err)
			}
		})
	}
}
