package input

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Every device and interface selected by a live portal EIS session is part
// of its authorization. A compositor pause or removal must retire the whole
// sender; unrelated EIS events must not revoke a different authorization.
func TestPortalSelectedEISDeviceAndInterfaceLossRevokesAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message eiMessage
	}{
		{"keyboard device removed", eiMessage{object: 0xff00000000000021, opcode: 0}},
		{"button device paused", eiMessage{object: 0xff00000000000022, opcode: 8}},
		{"scroll device removed", eiMessage{object: 0xff00000000000023, opcode: 0}},
		{"text device paused", eiMessage{object: 0xff00000000000024, opcode: 8}},
		{"pointer device removed", eiMessage{object: 0xff00000000000025, opcode: 0}},
		{"pointer device paused", eiMessage{object: 0xff00000000000025, opcode: 8}},
		{"keyboard interface removed", eiMessage{object: 101, opcode: 0}},
		{"button interface removed", eiMessage{object: 102, opcode: 0}},
		{"scroll interface removed", eiMessage{object: 103, opcode: 0}},
		{"text interface removed", eiMessage{object: 104, opcode: 0}},
		{"pointer interface removed", eiMessage{object: 105, opcode: 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _, client := newSyntheticRevocableRemoteDesktop(t)
			b.sender.keyboardDev = 0xff00000000000021
			b.sender.buttonDev = 0xff00000000000022
			b.sender.scrollDev = 0xff00000000000023
			b.sender.textDev = 0xff00000000000024
			b.sender.pointers = []eiPointerDevice{{device: 0xff00000000000025, pointer: 105, regions: []eiRegion{{width: 128, height: 128, scale: 1}}}}
			// An event on an unrelated device or a global control object
			// cannot revoke a live portal session.
			client.messages <- eiMessage{object: 0, opcode: 0}
			client.messages <- eiMessage{object: 0xff00000000000090, opcode: 8}
			client.messages <- eiMessage{object: 105, opcode: 3}
			select {
			case <-b.revoked:
				t.Fatal("unrelated EIS event revoked session")
			case <-time.After(5 * time.Millisecond):
			}
			if err := b.stateError(); err != nil {
				t.Fatalf("unrelated event invalidated authorization: %v", err)
			}
			client.messages <- tc.message
			select {
			case <-b.revoked:
			case <-time.After(time.Second):
				t.Fatal("selected device lifecycle loss did not revoke portal")
			}
			if err := b.stateError(); !errors.Is(err, ErrRemoteDesktopRevoked) || !strings.Contains(err.Error(), "EIS session ended") {
				t.Fatalf("wrong revocation after %s: %v", tc.name, err)
			}
			if err := b.KeyDown(context.Background(), "a"); !errors.Is(err, ErrRemoteDesktopRevoked) {
				t.Fatalf("revoked EIS sender accepted input: %v", err)
			}
			select {
			case <-b.writerDone:
			case <-time.After(time.Second):
				t.Fatal("EIS writer remained active after revocation")
			}
		})
	}
}
