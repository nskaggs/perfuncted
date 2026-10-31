//go:build linux

package input

import (
	"context"
	"strings"
	"testing"
)

// A release for a modifier not owned by this caller is not safe to inject:
// it can clear a shortcut being held by another desktop input client.
func TestUinputRejectsUnheldModifierReleaseWithoutEmittingEvents(t *testing.T) {
	for _, name := range []string{"ctrl", "shift", "alt", "super"} {
		t.Run(name, func(t *testing.T) {
			b, kb := newTestBackend(t)
			err := b.KeyUp(context.Background(), name)
			if err == nil || !strings.Contains(err.Error(), "not held") {
				t.Fatalf("unheld %s KeyUp result=%v", name, err)
			}
			if len(kb.events) > 0 {
				t.Fatalf("unheld modifier injected key-up: %v", kb.events)
			}
		})
	}
}

// A malformed tail must invalidate the complete command, not merely fail
// after it has already typed a prefix into the active application.
func TestUinputRejectsUnheldModifierTailBeforeTypingPrefix(t *testing.T) {
	for _, tc := range []struct{ name, command string }{
		{"literal prefix", "hello{ctrl up}"},
		{"chord prefix", "{ctrl+a}{shift up}"},
		{"double release", "{ctrl down}{ctrl up}{ctrl up}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, kb := newTestBackend(t)
			err := b.Type(context.Background(), tc.command)
			if err == nil || !strings.Contains(err.Error(), "not held") {
				t.Fatalf("unheld sequence %q accepted: %v", tc.command, err)
			}
			if len(kb.events) != 0 {
				t.Fatalf("failed sequence %q typed partially: %v", tc.command, kb.events)
			}
		})
	}
}

func TestUinputPreflightPreservesHeldModifierAndValidDownUpSequence(t *testing.T) {
	b, kb := newTestBackend(t)
	if err := b.KeyDown(context.Background(), "ctrl"); err != nil {
		t.Fatal(err)
	}
	if err := b.Type(context.Background(), "hello{ctrl+s}{ctrl up}"); err != nil {
		t.Fatalf("valid held sequence rejected: %v", err)
	}
	if b.modifierHeld(29) {
		t.Fatal("ctrl remained held after explicit release")
	}
	if len(kb.events) == 0 {
		t.Fatal("valid sequence emitted no keyboard events")
	}
}
