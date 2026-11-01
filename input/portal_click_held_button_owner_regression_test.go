//go:build linux

package input

import (
	"context"
	"encoding/binary"
	"strings"
	"testing"
)

// A click must not consume a button the caller explicitly holds for a drag.
// Reject before pointer movement or a second down/up pair reaches EIS.
func TestRemoteDesktopClickNeverReleasesCallerHeldButton(t *testing.T) {
	b, peer := newWireObservedRemoteDesktop(t)
	b.sender.pointers = []eiPointerDevice{{device: 701, pointer: 702, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}}}
	ctx := context.Background()
	if err := b.MouseDown(ctx, 1); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := readSyntheticEISFrame(peer); err != nil {
			t.Fatalf("initial button-down wire frame %d: %v", i, err)
		}
	}
	if !b.buttons[0x110] {
		t.Fatal("initial button-down state missing")
	}
	err := b.MouseClick(ctx, 30, 40, 1)
	if err == nil || !strings.Contains(err.Error(), "already held") {
		t.Fatalf("click consumed the caller-held drag: %v", err)
	}
	assertNoEIWireRequest(t, peer)
	if !b.buttons[0x110] || b.hasPointer {
		t.Fatalf("rejected click changed held state or pointer: buttons=%v pointer=%v", b.buttons, b.hasPointer)
	}
	if stateErr := b.stateError(); stateErr != nil {
		t.Fatalf("local validation revoked session: %v", stateErr)
	}
	if err := b.MouseUp(ctx, 1); err != nil {
		t.Fatalf("caller cannot release held drag after rejection: %v", err)
	}
	frame, err := readSyntheticEISFrame(peer)
	if err != nil || frame.object != b.sender.button || frame.opcode != 1 || len(frame.body) < 8 || binary.NativeEndian.Uint32(frame.body[4:8]) != 0 {
		t.Fatalf("held drag release not transmitted: frame=%+v err=%v", frame, err)
	}
	if _, err := readSyntheticEISFrame(peer); err != nil {
		t.Fatalf("release sync absent: %v", err)
	}
	if len(b.buttons) != 0 {
		t.Fatalf("button still held after legitimate release: %v", b.buttons)
	}
	if err := b.MouseClick(ctx, 30, 40, 1); err != nil {
		t.Fatalf("non-held click incorrectly rejected: %v", err)
	}
}

// Different buttons remain independently owned: a right click while the left
// button is held must preserve the left drag across both EIS transitions.
func TestRemoteDesktopClickOtherButtonPreservesIndependentDrag(t *testing.T) {
	b, peer := newWireObservedRemoteDesktop(t)
	b.sender.pointers = []eiPointerDevice{{device: 701, pointer: 702, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}}}
	ctx := context.Background()
	if err := b.MouseDown(ctx, 1); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := readSyntheticEISFrame(peer); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.MouseClick(ctx, 15, 35, 3); err != nil {
		t.Fatalf("other-button click rejected: %v", err)
	}
	if !b.buttons[0x110] || b.buttons[0x111] || !b.hasPointer || b.pointerX != 15 || b.pointerY != 35 {
		t.Fatalf("other-button click changed drag or pointer: buttons=%v pointer=(%d,%d) valid=%v", b.buttons, b.pointerX, b.pointerY, b.hasPointer)
	}
	var rightDown, rightUp, leftUp int
	for i := 0; i < 6; i++ {
		frame, err := readSyntheticEISFrame(peer)
		if err != nil {
			t.Fatalf("click wire event %d absent: %v", i, err)
		}
		if frame.object != b.sender.button || frame.opcode != 1 || len(frame.body) < 8 {
			continue
		}
		code := binary.NativeEndian.Uint32(frame.body[:4])
		down := binary.NativeEndian.Uint32(frame.body[4:8]) != 0
		if code == 0x111 {
			if down {
				rightDown++
			} else {
				rightUp++
			}
		}
		if code == 0x110 && !down {
			leftUp++
		}
	}
	if rightDown != 1 || rightUp != 1 || leftUp != 0 {
		t.Fatalf("unexpected EIS button transitions right=%d/%d left-up=%d", rightDown, rightUp, leftUp)
	}
	if err := b.MouseUp(ctx, 1); err != nil {
		t.Fatalf("left drag could not release after right click: %v", err)
	}
	if b.buttons[0x110] {
		t.Fatal("left remained held after explicit release")
	}
}
