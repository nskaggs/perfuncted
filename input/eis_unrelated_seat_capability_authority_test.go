package input

import (
	"encoding/binary"
	"testing"
)

// EIS global events are delivered on one stream. A forged or stale capability
// event from an unrelated seat must not mutate the negotiated seat's device
// mask or prematurely finish its capability enumeration.
func TestEISSetupIgnoresOtherSeatCapabilityAndCompletionEvents(t *testing.T) {
	server, ctx, cancel, result := startObservedEISenderSetup(t)
	defer cancel()
	awaitEISetupWait(t, ctx, 1)
	if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
		t.Fatal(err)
	}
	assertEISenderAnnouncesPingPong(t, server)
	awaitEISetupWait(t, ctx, 2)
	const connection = eiServerIDFloor + 16
	const ping = eiServerIDFloor + 17
	assertEISenderAnswersPing(t, server, connection, ping)
	awaitEISetupWait(t, ctx, 3)
	const seat = eiServerIDFloor + 32
	const untrustedSeat = eiServerIDFloor + 33
	if _, err := server.Write(eiRequest(connection, 1, eiU64(seat))); err != nil {
		t.Fatal(err)
	}
	awaitEISetupWait(t, ctx, 4)
	fake := append(eiRequest(untrustedSeat, 2, append(eiU64(0xffffffff), eiString("ei_keyboard")...)), eiRequest(untrustedSeat, 3, nil)...)
	if _, err := server.Write(fake); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"ei_pointer_absolute", "ei_button", "ei_keyboard"} {
		if _, err := server.Write(eiRequest(seat, 2, append(eiU64(1<<i), eiString(name)...))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := server.Write(eiRequest(seat, 3, nil)); err != nil {
		t.Fatal(err)
	}
	bind := readEIRequest(t, server)
	if bind.object != seat || bind.opcode != 1 || len(bind.body) != 8 || binary.NativeEndian.Uint64(bind.body) != 7 {
		t.Fatalf("foreign seat influenced bound capability mask: %+v", bind)
	}
	sync := readEIRequest(t, server)
	if sync.object != connection || sync.opcode != 0 {
		t.Fatalf("foreign seat caused invalid synchronization: %+v", sync)
	}
	awaitEISetupWait(t, ctx, 5)
	cancel()
	assertEISSetupCanceled(t, result)
}
