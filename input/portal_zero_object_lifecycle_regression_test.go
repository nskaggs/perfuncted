package input

import (
	"testing"
	"time"
)

// Absent optional interfaces are represented by object ID zero. Object zero
// is a global EIS protocol object and must never be confused with a device
// teardown merely because an optional device was not offered by the server.
func TestRemoteDesktopUnboundOptionalDeviceDoesNotRevokeOnGlobalObjectZero(t *testing.T) {
	backend, _, client := newSyntheticRevocableRemoteDesktop(t)
	backend.sender.scroll, backend.sender.scrollDev = 0, 0
	backend.sender.text, backend.sender.textDev = 0, 0
	if backend.isEISLifecycleFailure(eiMessage{object: 0, opcode: 0}) {
		t.Fatal("global EIS object zero misidentified as unbound device removal")
	}
	client.messages <- eiMessage{object: 0, opcode: 0}
	select {
	case <-backend.revoked:
		t.Fatal("unrelated global EIS event revoked live portal authorization")
	case <-time.After(40 * time.Millisecond):
	}
	if err := backend.stateError(); err != nil {
		t.Fatalf("global event invalidated authorized session: %v", err)
	}
	client.messages <- eiMessage{object: backend.sender.keyboardDev, opcode: 8}
	select {
	case <-backend.revoked:
	case <-time.After(time.Second):
		t.Fatal("real selected device suspension did not revoke access")
	}
}
