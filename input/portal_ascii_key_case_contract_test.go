//go:build linux

package input

import (
	"context"
	"encoding/binary"
	"testing"
)

func TestRemoteDesktopASCIIKeyCaseAliasesEmitTheSameEISCode(t *testing.T) {
	for _, key := range []string{"a", "A"} {
		t.Run(key, func(t *testing.T) {
			backend, peer := newWireObservedRemoteDesktop(t)
			wantCode, err := remoteKeyCode("a")
			if err != nil {
				t.Fatal(err)
			}
			if err := backend.KeyDown(context.Background(), key); err != nil {
				t.Fatalf("KeyDown(%q): %v", key, err)
			}
			request, err := readSyntheticEISFrame(peer)
			if err != nil || request.object != backend.sender.keyboard || request.opcode != 1 || len(request.body) != 8 {
				t.Fatalf("keyboard event = %+v, %v", request, err)
			}
			if got := binary.NativeEndian.Uint32(request.body[:4]); got != wantCode {
				t.Fatalf("KeyDown(%q) sent code %d, want lowercase alias code %d", key, got, wantCode)
			}
			if got := binary.NativeEndian.Uint32(request.body[4:]); got != 1 {
				t.Fatalf("KeyDown(%q) state = %d, want pressed", key, got)
			}
			if _, err := readSyntheticEISFrame(peer); err != nil {
				t.Fatalf("read key-down frame: %v", err)
			}
			if err := backend.KeyUp(context.Background(), key); err != nil {
				t.Fatalf("KeyUp(%q): %v", key, err)
			}
			request, err = readSyntheticEISFrame(peer)
			if err != nil || request.object != backend.sender.keyboard || request.opcode != 1 || len(request.body) != 8 ||
				binary.NativeEndian.Uint32(request.body[:4]) != wantCode || binary.NativeEndian.Uint32(request.body[4:]) != 0 {
				t.Fatalf("keyboard release = %+v, %v", request, err)
			}
			if _, err := readSyntheticEISFrame(peer); err != nil {
				t.Fatalf("read key-up frame: %v", err)
			}
			if len(backend.heldKeys) != 0 {
				t.Fatalf("case alias left key owned after release: %v", backend.heldKeys)
			}
		})
	}
}
