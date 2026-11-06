package input

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// EIS devices have independent interface objects, but a device containing
// multiple interfaces must receive just one emulation-start request. Optional
// absent devices are not authorized targets and must not receive any request.
func TestEISDeviceActivationStartsEachAuthorizedDeviceOnceOnWire(t *testing.T) {
	local, remote := unixConnectionPair(t)
	defer remote.Close()
	if err := remote.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	client := &eiClient{conn: local}
	client.serial.Store(0x10203040)
	sender := &eiSender{
		client:      client,
		keyboardDev: eiServerIDFloor + 0x10,
		buttonDev:   eiServerIDFloor + 0x10,
		scrollDev:   0,
		textDev:     0,
		pointers: []eiPointerDevice{
			{device: eiServerIDFloor + 0x20, pointer: eiServerIDFloor + 0x21},
			{device: eiServerIDFloor + 0x20, pointer: eiServerIDFloor + 0x21},
		},
	}
	if err := sender.selectDevices(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := local.Close(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []uint64{sender.keyboardDev, sender.pointers[0].device} {
		msg, err := readSyntheticEISFrame(remote)
		if err != nil {
			t.Fatalf("read authorized device start %x: %v", want, err)
		}
		if msg.object != want || msg.opcode != 1 || len(msg.body) != 8 || binary.NativeEndian.Uint32(msg.body[:4]) != 0x10203040 || binary.NativeEndian.Uint32(msg.body[4:]) != 1 {
			t.Fatalf("unexpected emulation activation of device %x: %+v", want, msg)
		}
	}
	if msg, err := readSyntheticEISFrame(remote); !errors.Is(err, io.EOF) {
		t.Fatalf("duplicate, absent, or unauthorized device was started: %+v err=%v", msg, err)
	}
}

// Cancellation and a broken EIS transport must terminate activation instead
// of allowing a partially configured input session to appear authorized.
func TestEISDeviceActivationRejectsCanceledAndBrokenSocket(t *testing.T) {
	t.Run("canceled before first write", func(t *testing.T) {
		local, remote := unixConnectionPair(t)
		defer remote.Close()
		defer local.Close()
		if err := remote.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		sender := &eiSender{client: &eiClient{conn: local}, keyboardDev: eiServerIDFloor + 0x10, buttonDev: eiServerIDFloor + 0x20}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := sender.selectDevices(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled selection = %v", err)
		}
		if err := local.Close(); err != nil {
			t.Fatal(err)
		}
		if msg, err := readSyntheticEISFrame(remote); !errors.Is(err, io.EOF) {
			t.Fatalf("canceled activation emitted frame %+v, err=%v", msg, err)
		}
	})
	t.Run("peer disconnect before activation", func(t *testing.T) {
		local, remote := unixConnectionPair(t)
		defer local.Close()
		if err := remote.Close(); err != nil {
			t.Fatal(err)
		}
		sender := &eiSender{client: &eiClient{conn: local}, keyboardDev: eiServerIDFloor + 0x10, buttonDev: eiServerIDFloor + 0x20}
		if err := sender.selectDevices(context.Background()); err == nil || !strings.Contains(err.Error(), "start device emulation") {
			t.Fatalf("lost peer was accepted as authorized EIS activation: %v", err)
		}
	})
}

// Seat capabilities arrive over an untrusted protocol socket. The client must
// reject malformed masks and interface names before binding a seat or starting
// any emulated input device; revocation while capabilities are arriving is
// terminal, rather than an ignorable event from a different object.
func TestEISSeatNegotiationRejectsMalformedCapabilitiesAndRevocation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		packet  func(connection, seat uint64) []byte
		want    string
		revoked bool
	}{
		{"truncated capability mask", func(_, seat uint64) []byte { return eiRequest(seat, 2, nil) }, "input/eis", false},
		{"missing capability name", func(_, seat uint64) []byte { return eiRequest(seat, 2, eiU64(1)) }, "input/eis", false},
		{"invalid capability utf8", func(_, seat uint64) []byte {
			return eiRequest(seat, 2, append(eiU64(1), eiString(string([]byte{0xff}))...))
		}, "invalid UTF-8", false},
		{"connection revoked during capability negotiation", func(connection, _ uint64) []byte { return eiRequest(connection, 0, nil) }, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, ctx, cancel, done := startEISenderSetup(t)
			defer cancel()
			if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
				t.Fatal(err)
			}
			assertEISenderAnnouncesPingPong(t, server)
			const connection = eiServerIDFloor + 0x71
			const seat = eiServerIDFloor + 0x72
			connectionBody := append(append(eiU32(6), eiU64(connection)...), eiU32(1)...)
			stream := eiRequest(0, 2, connectionBody)
			stream = append(stream, eiRequest(connection, 1, eiU64(seat))...)
			stream = append(stream, tc.packet(connection, seat)...)
			if _, err := server.Write(stream); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if tc.revoked {
					if !errors.Is(err, ErrRemoteDesktopRevoked) {
						t.Fatalf("revoked seat still authorized: %v", err)
					}
				} else if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("malformed seat capability accepted, error=%v, want %q", err, tc.want)
				}
			case <-ctx.Done():
				t.Fatalf("failed to reject malformed seat before deadline: %v", ctx.Err())
			}
		})
	}
}
