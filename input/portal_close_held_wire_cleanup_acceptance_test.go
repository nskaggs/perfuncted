//go:build linux

package input

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// With a real private EIS Unix transport, a clean portal close must release
// every held input before sending a single device stop, even when keyboard,
// button and scroll interfaces all share the same EIS device.
func TestRemoteDesktopCloseReleasesHeldKeysAndButtonsAndStopsSharedDeviceOnce(t *testing.T) {
	conn, peer := unixConnectionPair(t)
	defer peer.Close()
	if err := peer.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	client := &eiClient{conn: conn, messages: make(chan eiMessage, 8), done: make(chan struct{}), stop: make(chan struct{})}
	dev := uint64(eiServerIDFloor + 0x32)
	sender := &eiSender{client: client, connection: eiServerIDFloor + 0x30, seat: eiServerIDFloor + 0x31, keyboard: 101, button: 102, scroll: 103, text: 104, keyboardDev: dev, buttonDev: dev, scrollDev: dev, textDev: dev}
	portal := &portalSession{owner: ":1.44", path: dbus.ObjectPath("/org/freedesktop/portal/desktop/session/fixture"), signals: make(chan *dbus.Signal, 8)}
	b := newRemoteDesktopBackend(sender, portal)
	defer b.Close()
	if err := b.KeyDown(context.Background(), "ctrl"); err != nil {
		t.Fatal(err)
	}
	if err := b.MouseDown(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := readSyntheticEISFrame(peer); err != nil {
			t.Fatalf("reading original input event %d: %v", i, err)
		}
	}
	if len(b.heldKeys) != 1 || len(b.buttons) != 1 {
		t.Fatalf("test did not establish held input: keys=%v buttons=%v", b.heldKeys, b.buttons)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	releaseKey, releaseButton, stopCount := 0, 0, 0
	for i := 0; i < 5; i++ {
		frame, err := readSyntheticEISFrame(peer)
		if err != nil {
			t.Fatalf("reading close frame %d: %v", i, err)
		}
		switch {
		case frame.object == sender.keyboard && frame.opcode == 1 && len(frame.body) >= 8 && binary.NativeEndian.Uint32(frame.body[4:8]) == 0:
			releaseKey++
		case frame.object == sender.button && frame.opcode == 1 && len(frame.body) >= 8 && binary.NativeEndian.Uint32(frame.body[4:8]) == 0:
			releaseButton++
		case frame.object == dev && frame.opcode == 2:
			stopCount++
		case frame.object == dev && frame.opcode == 3:
			// Synchronized key/button release frame
		default:
			t.Fatalf("unexpected frame during portal close %+v", frame)
		}
	}
	if releaseKey != 1 || releaseButton != 1 || stopCount != 1 {
		t.Fatalf("unbalanced close traffic: key=%d button=%d device stop=%d", releaseKey, releaseButton, stopCount)
	}
	if len(b.heldKeys) != 0 || len(b.buttons) != 0 {
		t.Fatalf("released keys/buttons still recorded: keys=%v buttons=%v", b.heldKeys, b.buttons)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readSyntheticEISFrame(peer); !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("close emitted extra message or left writer alive: %v", err)
	}
}
