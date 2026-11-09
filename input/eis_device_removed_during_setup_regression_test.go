package input

import (
	"errors"
	"testing"
	"time"
)

// An EIS device that disappears during setup no longer authorizes any of
// its advertised input capabilities. Setup must fail closed rather than
// retaining a previously completed and resumed device.
func TestEISDeviceRemovalDuringSetupRevokesWithoutPublishingSender(t *testing.T) {
	server, ctx, done := prepareEISInvalidGrantEndpoint(t)
	const seat = eiServerIDFloor + 0x11
	const device = eiServerIDFloor + 0x12
	stream := eiRequest(seat, 4, append(eiU64(device), eiU32(1)...))
	stream = append(stream, eiRequest(device, 5, append(eiU64(eiServerIDFloor+0x31), eiString("ei_keyboard")...))...)
	stream = append(stream, eiRequest(device, 6, nil)...)
	stream = append(stream, eiRequest(device, 7, eiU32(1))...)
	stream = append(stream, eiRequest(device, 0, nil)...)
	if _, err := server.Write(stream); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.sender != nil || !errors.Is(result.err, ErrRemoteDesktopRevoked) {
			t.Fatalf("destroyed device retained authorization: sender=%v err=%v", result.sender, result.err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatalf("destroyed device left startup waiting: %v", ctx.Err())
	}
}
