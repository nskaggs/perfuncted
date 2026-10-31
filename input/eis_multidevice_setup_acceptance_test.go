package input

import (
	"context"
	"encoding/binary"
	"testing"
	"time"
)

func TestEISSetupSelectsCapabilitiesAcrossResumedDevices(t *testing.T) {
	client, server := unixConnectionPair(t)
	t.Cleanup(func() { _ = server.Close() })
	if err := server.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}

	const (
		connectionID = eiServerIDFloor + 0x10
		seatID       = eiServerIDFloor + 0x11
		pointerID    = eiServerIDFloor + 0x20
		keyboardID   = eiServerIDFloor + 0x21
	)
	stream := eiRequest(0, 0, eiU32(1))
	connection := append(eiU32(4), eiU64(connectionID)...)
	connection = append(connection, eiU32(1)...)
	stream = append(stream, eiRequest(0, 2, connection)...)
	stream = append(stream, eiRequest(connectionID, 1, eiU64(seatID))...)
	for i, capability := range []string{"ei_pointer_absolute", "ei_button", "ei_keyboard"} {
		body := append(eiU64(uint64(1<<i)), eiString(capability)...)
		stream = append(stream, eiRequest(seatID, 2, body)...)
	}
	stream = append(stream, eiRequest(seatID, 3, nil)...)
	stream = append(stream, eiRequest(seatID, 4, append(eiU64(pointerID), eiU32(1)...))...)
	stream = append(stream, eiRequest(pointerID, 5, append(eiU64(eiServerIDFloor+0x30), eiString("ei_pointer_absolute")...))...)
	region := append(eiU32(10), eiU32(20)...)
	region = append(region, eiU32(200)...)
	region = append(region, eiU32(120)...)
	region = append(region, eiF32(1)...)
	stream = append(stream, eiRequest(pointerID, 4, region)...)
	stream = append(stream, eiRequest(pointerID, 6, nil)...)
	stream = append(stream, eiRequest(pointerID, 7, eiU32(5))...)
	stream = append(stream, eiRequest(seatID, 4, append(eiU64(keyboardID), eiU32(1)...))...)
	stream = append(stream, eiRequest(keyboardID, 5, append(eiU64(eiServerIDFloor+0x31), eiString("ei_keyboard")...))...)
	stream = append(stream, eiRequest(keyboardID, 5, append(eiU64(eiServerIDFloor+0x32), eiString("ei_button")...))...)
	stream = append(stream, eiRequest(keyboardID, 6, nil)...)
	stream = append(stream, eiRequest(keyboardID, 7, eiU32(6))...)
	stream = append(stream, eiRequest(1, 0, eiU64(0))...)
	for len(stream) > 0 {
		n, err := server.Write(stream)
		if err != nil {
			t.Fatalf("write synthetic multi-device setup: %v", err)
		}
		stream = stream[n:]
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	sender, err := setupEISender(ctx, client)
	if err != nil {
		t.Fatalf("setup EIS sender from split capabilities: %v", err)
	}
	t.Cleanup(func() { _ = sender.client.close() })
	if sender.keyboardDev != keyboardID || sender.buttonDev != keyboardID || sender.pointerDev != pointerID {
		t.Fatalf("capabilities selected across resumed devices incorrectly: keyboard=%x button=%x pointer=%x", sender.keyboardDev, sender.buttonDev, sender.pointerDev)
	}
	if sender.scroll != 0 || sender.text != 0 || len(sender.pointers) != 1 || !sender.validatePoint(20, 30) {
		t.Fatalf("optional capabilities or pointer region are incorrect: scroll=%x text=%x pointers=%+v", sender.scroll, sender.text, sender.pointers)
	}
	for i := 0; i < 14; i++ {
		request, err := readSyntheticEISFrame(server)
		if err != nil {
			t.Fatalf("read sender negotiation request %d: %v", i, err)
		}
		if request.object != 0 {
			t.Fatalf("negotiation request %d used unexpected object %x", i, request.object)
		}
		if i == 13 && request.opcode != 1 {
			t.Fatalf("last negotiation request opcode = %d, want handshake finish", request.opcode)
		}
	}
	if request, err := readSyntheticEISFrame(server); err != nil || request.object != seatID || request.opcode != 1 {
		t.Fatalf("seat capabilities were not bound: %+v %v", request, err)
	}
	if request, err := readSyntheticEISFrame(server); err != nil || request.object != connectionID || request.opcode != 0 {
		t.Fatalf("device setup synchronization was not requested: %+v %v", request, err)
	}
	for _, want := range []uint64{keyboardID, pointerID} {
		request, err := readSyntheticEISFrame(server)
		if err != nil {
			t.Fatalf("read selected device start: %v", err)
		}
		if request.object != want || request.opcode != 1 || len(request.body) != 8 || binary.NativeEndian.Uint32(request.body[4:8]) != 1 {
			t.Fatalf("unexpected selected-device start = %+v, want device %x sequence 1", request, want)
		}
	}
}
