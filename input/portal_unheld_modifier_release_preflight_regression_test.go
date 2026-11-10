//go:build linux

package input

import (
	"context"
	"encoding/binary"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// An EIS serial is an inbound server value, not a count of outbound requests.
// Observe the real private Unix socket to prove a rejected command sent no bytes.
func newWireObservedRemoteDesktop(t *testing.T) (*RemoteDesktopBackend, *net.UnixConn) {
	t.Helper()
	connection, peer := unixConnectionPair(t)
	t.Cleanup(func() { _ = peer.Close() })
	client := &eiClient{conn: connection, messages: make(chan eiMessage, 8), done: make(chan struct{}), stop: make(chan struct{})}
	device := eiServerIDFloor + 0x21
	sender := &eiSender{client: client, connection: eiServerIDFloor + 0x10, seat: eiServerIDFloor + 0x11,
		keyboard: 101, button: 102, scroll: 103, text: 104, keyboardDev: device, buttonDev: device, scrollDev: device, textDev: device}
	b := newRemoteDesktopBackend(sender, &portalSession{})
	t.Cleanup(func() { _ = b.Close() })
	return b, peer
}

func assertNoEIWireRequest(t *testing.T, peer *net.UnixConn) {
	t.Helper()
	if err := peer.SetReadDeadline(time.Now().Add(15 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var value [1]byte
	n, err := peer.Read(value[:])
	if err := peer.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	if n != 0 || !os.IsTimeout(err) {
		t.Fatalf("rejected command emitted %d EIS bytes (first=%x): %v", n, value, err)
	}
}

// Every Type command is a logical batch: an unheld modifier release at the
// tail must fail before the earlier text or accelerator is dispatched. The
// check must run on the serialized EIS writer, which owns heldKeys.
func TestRemoteDesktopRejectsUnheldModifierReleaseBeforeBatchDispatch(t *testing.T) {
	for _, tc := range []struct{ name, sequence string }{
		{"stray ctrl", "{ctrl up}"},
		{"literal prefix", "private{ctrl up}"},
		{"named prefix", "{enter}{shift up}"},
		{"accelerator prefix", "{ctrl+a}{alt up}"},
		{"different held modifier", "{ctrl down}{shift up}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, peer := newWireObservedRemoteDesktop(t)
			err := b.Type(context.Background(), tc.sequence)
			if err == nil || !strings.Contains(err.Error(), "not held") {
				t.Fatalf("unauthorized release accepted: err=%v", err)
			}
			assertNoEIWireRequest(t, peer)
			if len(b.heldKeys) != 0 {
				t.Fatalf("invalid command changed held keys: %v", b.heldKeys)
			}
			if err := b.stateError(); err != nil {
				t.Fatalf("validation error revoked authorized portal: %v", err)
			}
		})
	}
}

func TestRemoteDesktopAcceptsBalancedAndCallerHeldModifierRelease(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	if err := b.Type(context.Background(), "{ctrl down}{ctrl up}"); err != nil {
		t.Fatalf("balanced modifier sequence rejected: %v", err)
	}
	if len(b.heldKeys) != 0 {
		t.Fatalf("balanced sequence stranded keys: %v", b.heldKeys)
	}
	if err := b.KeyDown(context.Background(), "shift"); err != nil {
		t.Fatal(err)
	}
	if err := b.Type(context.Background(), "{shift up}"); err != nil {
		t.Fatalf("caller-owned modifier release rejected: %v", err)
	}
	if len(b.heldKeys) != 0 {
		t.Fatalf("caller-owned modifier not released: %v", b.heldKeys)
	}
}

func TestRemoteDesktopKeyUpRejectsUnheldModifierWithoutEISWireWrites(t *testing.T) {
	for _, name := range []string{"ctrl", "shift", "alt", "super"} {
		t.Run(name, func(t *testing.T) {
			b, peer := newWireObservedRemoteDesktop(t)
			err := b.KeyUp(context.Background(), name)
			if err == nil || !strings.Contains(err.Error(), "not held") {
				t.Fatalf("unowned modifier release accepted: %v", err)
			}
			assertNoEIWireRequest(t, peer)
			if len(b.heldKeys) != 0 {
				t.Fatalf("rejected modifier release changed held keys")
			}
			if err := b.KeyDown(context.Background(), name); err != nil {
				t.Fatalf("valid down failed: %v", err)
			}
			if err := b.KeyUp(context.Background(), name); err != nil {
				t.Fatalf("valid up failed: %v", err)
			}
			if len(b.heldKeys) != 0 {
				t.Fatalf("legitimate release retained held state: %v", b.heldKeys)
			}
			for i, down := range []bool{true, false} {
				key, err := readSyntheticEISFrame(peer)
				if err != nil || key.object != b.sender.keyboard || key.opcode != 1 || len(key.body) < 8 || (binary.NativeEndian.Uint32(key.body[4:8]) == 1) != down {
					t.Fatalf("valid key event %d not transmitted: %+v %v", i, key, err)
				}
				if frame, err := readSyntheticEISFrame(peer); err != nil || frame.object != b.sender.keyboardDev || frame.opcode != 3 {
					t.Fatalf("synchronized EIS key frame %d absent: %+v %v", i, frame, err)
				}
			}
		})
	}
}
