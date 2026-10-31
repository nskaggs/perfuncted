//go:build linux

package input

import (
	"context"
	"strings"
	"testing"

	"github.com/jezek/xgb/xproto"
)

// No XTEST input event may be injected for an unheld modifier: a release is a
// state transition owned by the caller, not permission to affect other users'
// keyboard state on the shared X server.
func TestXTestUnheldModifiersDoNotInjectKeyUpEvents(t *testing.T) {
	modifiers := []string{"shift", "ctrl", "alt", "super"}
	mapping := []xproto.Keysym{0xffe1, 0xffe3, 0xffe9, 0xffeb}
	for _, name := range modifiers {
		t.Run(name, func(t *testing.T) {
			backend, events := newTestXTestBackend(t, 1, mapping, nil)
			err := backend.KeyUp(context.Background(), name)
			if err == nil || !strings.Contains(err.Error(), "not held") {
				t.Fatalf("unheld modifier key-up accepted: %v", err)
			}
			if len(*events) != 0 {
				t.Fatalf("unheld modifier produced events: %+v", *events)
			}
		})
	}
}

func TestXTestUnheldModifierSyntaxDoesNotInjectKeyUpEvents(t *testing.T) {
	backend, events := newTestXTestBackend(t, 1, []xproto.Keysym{0xffe1, 0xffe3, 0xffe9, 0xffeb}, nil)
	err := backend.Type(context.Background(), "{ctrl up}")
	if err == nil || !strings.Contains(err.Error(), "not held") {
		t.Fatalf("unheld Ctrl syntax accepted: %v", err)
	}
	if len(*events) != 0 {
		t.Fatalf("unheld modifier syntax injected %+v", *events)
	}
}

// A malformed release at the tail must reject the entire command before a
// valid prefix can type into a different window.
func TestXTestUnheldModifierTailFailsBeforeTypingPrefix(t *testing.T) {
	backend, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61, 0xffe3}, nil)
	err := backend.Type(context.Background(), "a{ctrl up}")
	if err == nil || !strings.Contains(err.Error(), "not held") {
		t.Fatalf("invalid suffix did not fail: %v", err)
	}
	if len(*events) != 0 {
		t.Fatalf("invalid suffix left partially typed prefix: %+v", *events)
	}
}
