package input

import (
	"errors"
	"net"
	"testing"
	"time"
)

// A seat can be destroyed after capability binding but before any input
// devices are ready. The setup handshake must revoke on either side of its
// callback barrier instead of waiting for now-impossible device events.
func TestEISSeatRemovalDuringDeviceSetupRevokesImmediately(t *testing.T) {
	for _, tc := range []struct {
		name           string
		finishCallback bool
	}{
		{"before setup callback", false},
		{"after setup callback", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkEISSeatRemovalDuringDeviceSetup(t, tc.finishCallback)
		})
	}
}

func checkEISSeatRemovalDuringDeviceSetup(t *testing.T, finishCallback bool) {
	server, _, cancel, done := startEISenderWithResult(t)
	defer cancel()
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
	if bind.object != seat || bind.opcode != 1 || callback.object != connection || callback.opcode != 0 {
		t.Fatalf("unexpected setup requests: %+v %+v", bind, callback)
	}
	if finishCallback {
		completeEISSeatRemovalSetupCallback(t, server, callback)
	}
	if _, err := server.Write(eiRequest(seat, 0, nil)); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.sender != nil || !errors.Is(result.err, ErrRemoteDesktopRevoked) {
			t.Fatalf("removed seat retained authority: sender=%v err=%v", result.sender, result.err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("seat removal left setup waiting for device enumeration")
	}
}

func completeEISSeatRemovalSetupCallback(t *testing.T, server *net.UnixConn, callback testEIRequest) {
	t.Helper()
	callbackID, err := eiReadU64(callback.body, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Write(eiRequest(callbackID, 0, eiU64(0))); err != nil {
		t.Fatal(err)
	}
}
