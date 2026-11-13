package input

import "testing"

// EIS object zero identifies the global protocol, not a device. Destroying
// an unrelated object or sending an unrelated opcode must not revoke an
// otherwise authorized session. A selected device pause or destruction must.
func TestPortalLifecycleIgnoresGlobalAndUnrelatedObjects(t *testing.T) {
	sender := &eiSender{
		seat: 11, keyboard: 12, keyboardDev: 13,
		button: 14, buttonDev: 15,
		scroll: 16, scrollDev: 17,
		text: 18, textDev: 19,
		pointers: []eiPointerDevice{{pointer: 20, device: 21}},
	}
	b := &RemoteDesktopBackend{sender: sender}
	for _, tc := range []struct {
		name             string
		msg              eiMessage
		selectedPaused   bool
		lifecycleFailure bool
	}{
		{"global destroy is not device revocation", eiMessage{object: 0, opcode: 0}, false, false},
		{"global pause is not device revocation", eiMessage{object: 0, opcode: 8}, false, false},
		{"unrelated device destruction", eiMessage{object: 200, opcode: 0}, false, false},
		{"unrelated device pause", eiMessage{object: 200, opcode: 8}, false, false},
		{"selected keyboard pause", eiMessage{object: 13, opcode: 8}, true, true},
		{"selected button destroyed", eiMessage{object: 15, opcode: 0}, true, true},
		{"selected scroll pause", eiMessage{object: 17, opcode: 8}, true, true},
		{"selected text destroyed", eiMessage{object: 19, opcode: 0}, true, true},
		{"selected pointer pause", eiMessage{object: 21, opcode: 8}, true, true},
		{"selected pointer destroyed", eiMessage{object: 21, opcode: 0}, true, true},
		{"selected device unrelated opcode", eiMessage{object: 13, opcode: 7}, false, false},
		{"seat destruction", eiMessage{object: 11, opcode: 0}, false, true},
		{"keyboard object destruction", eiMessage{object: 12, opcode: 0}, false, true},
		{"button object destruction", eiMessage{object: 14, opcode: 0}, false, true},
		{"scroll object destruction", eiMessage{object: 16, opcode: 0}, false, true},
		{"text object destruction", eiMessage{object: 18, opcode: 0}, false, true},
		{"pointer object destruction", eiMessage{object: 20, opcode: 0}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := b.isSelectedDevicePaused(tc.msg); got != tc.selectedPaused {
				t.Fatalf("selected-device paused=%v want=%v", got, tc.selectedPaused)
			}
			if got := b.isEISLifecycleFailure(tc.msg); got != tc.lifecycleFailure {
				t.Fatalf("lifecycle failure=%v want=%v", got, tc.lifecycleFailure)
			}
		})
	}
}
