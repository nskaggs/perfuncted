//go:build linux

package input

import (
	"context"
	"strings"
	"testing"
)

// Duplicate key-down on an already-owned modifier must not cause an XTEST
// key repeat. A later KeyUp remains the sole release of the caller's hold.
func TestXTestDirectHeldModifierCannotBePressedTwice(t *testing.T) {
	b, events := newModifierXTestBackend(t)
	if err := b.KeyDown(context.Background(), "ctrl"); err != nil {
		t.Fatal(err)
	}
	initial := len(*events)
	if err := b.KeyDown(context.Background(), "ctrl"); err == nil || !strings.Contains(err.Error(), "already held") {
		t.Fatalf("duplicate ctrl down accepted: %v", err)
	}
	if len(*events) != initial {
		t.Fatalf("duplicate ctrl down emitted input: %+v", (*events)[initial:])
	}
	if err := b.KeyUp(context.Background(), "ctrl"); err != nil {
		t.Fatalf("original hold could not be released: %v", err)
	}
}

// Reject invalid modifier ownership transitions over the *entire* command
// before emitting a valid prefix, even when the invalid transition appears
// after text or an accelerator.
func TestXTestTypeRejectsDuplicateHeldModifierWithoutPartialInput(t *testing.T) {
	for _, tc := range []struct{ name, command string }{
		{"duplicate down", "a{ctrl down}{ctrl down}"},
		{"tap explicitly held modifier", "a{ctrl down}{ctrl}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, events := newModifierXTestBackend(t)
			err := b.Type(context.Background(), tc.command)
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("invalid command accepted: %v", err)
			}
			if len(*events) != 0 {
				t.Fatalf("failed command injected prefix: %+v", *events)
			}
		})
	}
}
