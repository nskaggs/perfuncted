//go:build linux

package input

import (
	"context"
	"strings"
	"testing"
)

func TestUinputNeverTapsHeldModifierWithoutExplicitRelease(t *testing.T) {
	for _, tc := range []struct{ name, initial, command string }{
		{"prior ctrl tap", "ctrl", "{ctrl}"},
		{"prior shift tap after text", "shift", "private{shift}"},
		{"prior alt repeat", "alt", "{alt down}"},
		{"same batch ctrl down then tap", "", "{ctrl down}{ctrl}"},
		{"same batch shift down then repeat", "", "{shift down}{shift down}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, keyboard := newTestBackend(t)
			if tc.initial != "" {
				if err := backend.KeyDown(context.Background(), tc.initial); err != nil {
					t.Fatal(err)
				}
			}
			before := len(keyboard.events)
			err := backend.Type(context.Background(), tc.command)
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("uinput accepted tap or repeat of held modifier: %v", err)
			}
			if len(keyboard.events) != before {
				t.Fatalf("denied batch injected %v", keyboard.events[before:])
			}
		})
	}
}

func TestUinputNeverRepeatsHeldModifierKeyDown(t *testing.T) {
	for _, mod := range []string{"ctrl", "shift", "alt", "super"} {
		t.Run(mod, func(t *testing.T) {
			backend, keyboard := newTestBackend(t)
			if err := backend.KeyDown(context.Background(), mod); err != nil {
				t.Fatal(err)
			}
			before := len(keyboard.events)
			err := backend.KeyDown(context.Background(), mod)
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("uinput repeated held modifier: %v", err)
			}
			if len(keyboard.events) != before {
				t.Fatalf("duplicate down injected %v", keyboard.events[before:])
			}
			if err := backend.KeyUp(context.Background(), mod); err != nil {
				t.Fatalf("original modifier cannot release: %v", err)
			}
		})
	}
}
