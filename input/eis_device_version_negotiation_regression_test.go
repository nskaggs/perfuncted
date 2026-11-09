package input

import (
	"strings"
	"testing"
	"time"
)

// The client announces support only for ei_device version 1. The compositor
// cannot grant a device at a newer unsupported protocol version.
func TestEISRejectsUnnegotiatedDeviceVersionOverWire(t *testing.T) {
	for _, version := range []uint32{2, 3, ^uint32(0)} {
		t.Run(string(rune('A'+version%26)), func(t *testing.T) {
			server, _, done := prepareEISInvalidGrantEndpoint(t)
			const seat = eiServerIDFloor + 0x11
			const device = eiServerIDFloor + 0x12
			if _, err := server.Write(eiRequest(seat, 4, append(eiU64(device), eiU32(version)...))); err != nil {
				t.Fatal(err)
			}
			select {
			case result := <-done:
				if result.sender != nil || result.err == nil || !strings.Contains(result.err.Error(), "invalid device") {
					t.Fatalf("unnegotiated device version %d accepted: sender=%v err=%v", version, result.sender, result.err)
				}
			case <-time.After(250 * time.Millisecond):
				t.Fatalf("unnegotiated device version %d left setup awaiting devices", version)
			}
		})
	}
}
