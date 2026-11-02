package input

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"testing"
)

// The synthetic compositor communicates over the same Unix wire protocol as
// the actual EIS server. A forged interface rebind during enumeration must
// abort setup before any device becomes available for input injection.
func TestEISHandshakeRejectsReboundOrZeroIdentityInterfacesOverRealUnixSocket(t *testing.T) {
	for _, tc := range []struct {
		name       string
		additional []byte
		want       string
	}{
		{"different keyboard object", eiRequest(eiServerIDFloor+0x12, 5, append(eiU64(102), eiString("ei_keyboard")...)), "duplicate interface"},
		{"replayed keyboard object", eiRequest(eiServerIDFloor+0x12, 5, append(eiU64(101), eiString("ei_keyboard")...)), "duplicate interface"},
		{"zero interface object", eiRequest(eiServerIDFloor+0x12, 5, append(eiU64(0), eiString("ei_button")...)), "invalid interface"},
		{"zero device version", eiRequest(eiServerIDFloor+0x11, 4, append(eiU64(eiServerIDFloor+0x12), eiU32(0)...)), "invalid device"},
		{"zero device object", eiRequest(eiServerIDFloor+0x11, 4, append(eiU64(0), eiU32(1)...)), "invalid device"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertEISWireRejectsInvalidAuthorization(t, tc.additional, tc.want)
		})
	}
}

func assertEISWireRejectsInvalidAuthorization(t *testing.T, additional []byte, want string) {
	t.Helper()
	server, ctx, done := prepareEISInvalidGrantEndpoint(t)
	const seat = eiServerIDFloor + 0x11
	const device = eiServerIDFloor + 0x12
	if want == "duplicate interface" || want == "invalid interface" {
		if _, err := server.Write(eiRequest(seat, 4, append(eiU64(device), eiU32(1)...))); err != nil {
			t.Fatal(err)
		}
		if _, err := server.Write(eiRequest(device, 5, append(eiU64(101), eiString("ei_keyboard")...))); err != nil {
			t.Fatal(err)
		}
	}
	// No end-of-enumeration callback is needed: a malformed input grant
	// must terminate setup immediately, before publishing any sender.
	if _, err := server.Write(additional); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.sender != nil || result.err == nil || !strings.Contains(result.err.Error(), want) {
			t.Fatalf("forged EIS authority accepted: sender=%v err=%v", result.sender, result.err)
		}
	case <-ctx.Done():
		t.Fatalf("invalid EIS grant not rejected promptly: %v", ctx.Err())
	}
}

func prepareEISInvalidGrantEndpoint(t *testing.T) (*net.UnixConn, context.Context, <-chan eisSetupResult) {
	t.Helper()
	server, ctx, cancel, done := startEISenderWithResult(t)
	t.Cleanup(cancel)
	if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
		t.Fatal(err)
	}
	assertEISenderAnnouncesPingPong(t, server)
	const connection = eiServerIDFloor + 0x10
	const seat = eiServerIDFloor + 0x11
	stream := eiRequest(0, 2, append(append(eiU32(7), eiU64(connection)...), eiU32(1)...))
	stream = append(stream, eiRequest(connection, 1, eiU64(seat))...)
	for i, name := range []string{"ei_pointer_absolute", "ei_button", "ei_keyboard"} {
		stream = append(stream, eiRequest(seat, 2, append(eiU64(uint64(1<<i)), eiString(name)...))...)
	}
	stream = append(stream, eiRequest(seat, 3, nil)...)
	if _, err := server.Write(stream); err != nil {
		t.Fatal(err)
	}
	bind := readEIRequest(t, server)
	callback := readEIRequest(t, server)
	if bind.object != seat || bind.opcode != 1 || callback.object != connection || callback.opcode != 0 || len(callback.body) < 8 {
		t.Fatalf("unexpected EIS negotiation: bind=%+v callback=%+v", bind, callback)
	}
	callbackID := binary.NativeEndian.Uint64(callback.body[:8])
	if callbackID == 0 {
		t.Fatal("negotiation selected a zero callback identity")
	}
	return server, ctx, done
}

// An object-zero seat is not an authorized ei_seat. Accepting it can send
// subsequent seat-binding requests on the handshake object instead.
func TestEISHandshakeRejectsZeroSeatWithoutBindingAnyCapabilities(t *testing.T) {
	server, ctx, cancel, done := startEISenderWithResult(t)
	defer cancel()
	if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
		t.Fatal(err)
	}
	assertEISenderAnnouncesPingPong(t, server)
	const connection = eiServerIDFloor + 0x10
	stream := eiRequest(0, 2, append(append(eiU32(7), eiU64(connection)...), eiU32(1)...))
	stream = append(stream, eiRequest(connection, 1, eiU64(0))...)
	// Explicit zero-seat completion triggers the old path to accept a
	// handshake-object capability response instead of rejecting the seat.
	stream = append(stream, eiRequest(0, 3, nil)...)
	if _, err := server.Write(stream); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.sender != nil || result.err == nil || !strings.Contains(result.err.Error(), "invalid seat") {
			t.Fatalf("zero EIS seat was not rejected at identity boundary: sender=%v err=%v", result.sender, result.err)
		}
	case <-ctx.Done():
		t.Fatalf("zero seat never rejected: %v", ctx.Err())
	}
}
