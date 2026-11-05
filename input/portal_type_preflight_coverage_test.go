package input

import (
	"strings"
	"testing"
)

func TestRemoteDesktopTypePreflightRejectsLateUnauthorizedActions(t *testing.T) {
	tests := []struct {
		name    string
		sender  eiSender
		actions []keySend
		want    string
	}{
		{"invalid UTF8 after key", eiSender{keyboard: 1, keyboardDev: 2, text: 3, textDev: 4}, []keySend{{key: "enter"}, {text: string([]byte{0xff})}}, "UTF-8"},
		{"missing text device after key", eiSender{keyboard: 1, keyboardDev: 2, text: 3}, []keySend{{key: "enter"}, {text: "hello"}}, "text input"},
		{"missing text object", eiSender{keyboard: 1, keyboardDev: 2, textDev: 4}, []keySend{{key: "enter"}, {text: "hello"}}, "text input"},
		{"missing keyboard device after text", eiSender{text: 1, textDev: 2, keyboard: 3}, []keySend{{text: "hello"}, {key: "enter"}}, "keyboard input"},
		{"unknown key after text", eiSender{text: 1, textDev: 2, keyboard: 3, keyboardDev: 4}, []keySend{{text: "hello"}, {key: "nonexistent-key-name"}}, "unknown key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := &RemoteDesktopBackend{sender: &tt.sender}
			got, err := backend.validatedTypeKeycodes(tt.actions)
			if err == nil || !strings.Contains(err.Error(), tt.want) || got != nil {
				t.Fatalf("preflight returned codes=%v error=%v, want error containing %q and no codes", got, err, tt.want)
			}
		})
	}
}

func TestRemoteDesktopTypePreflightAndHeldKeyOwnership(t *testing.T) {
	sender := &eiSender{keyboard: 1, keyboardDev: 2, text: 3, textDev: 4}
	shift, err := remoteKeyCode("shift")
	if err != nil {
		t.Fatal(err)
	}
	backend := &RemoteDesktopBackend{sender: sender, heldKeys: map[uint32]bool{shift: true}}
	tests := []struct {
		name    string
		actions []keySend
		want    string
	}{
		{"stray modifier release", []keySend{{key: "ctrl", up: true}}, "not held"},
		{"stray ordinary release", []keySend{{key: "enter", up: true}}, "not held"},
		{"duplicate press", []keySend{{key: "shift", down: true}}, "already held"},
		{"tap held modifier", []keySend{{key: "shift"}}, "already held"},
		{"late duplicate in batch", []keySend{{key: "enter", down: true}, {text: "hello"}, {key: "enter", down: true}}, "already held"},
		{"late stray release in batch", []keySend{{text: "hello"}, {key: "enter", up: true}}, "not held"},
		{"release then tap is allowed", []keySend{{key: "shift", up: true}, {key: "shift"}}, ""},
		{"press then release is allowed", []keySend{{key: "enter", down: true}, {key: "enter", up: true}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			codes, err := backend.validatedTypeKeycodes(tt.actions)
			if err != nil {
				t.Fatalf("preflight: %v", err)
			}
			err = backend.validateTypeHeldSequence(tt.actions, codes)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("valid sequence rejected: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("sequence error = %v, want %q", err, tt.want)
			}
			if !backend.heldKeys[shift] || len(backend.heldKeys) != 1 {
				t.Fatalf("preflight mutated held-key ownership: %v", backend.heldKeys)
			}
		})
	}
}
