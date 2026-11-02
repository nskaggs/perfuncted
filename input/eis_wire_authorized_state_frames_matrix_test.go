package input

import (
	"encoding/binary"
	"errors"
	"testing"
)

// A synthetic sender's negotiated serial, authorized object IDs and state
// transitions must be present on the actual EIS wire, not just in backend
// bookkeeping. Each valid state sends one device-frame acknowledgment.
func TestEIKeyAndButtonFrameWireAuthorizationMatrix(t *testing.T) {
	client := &eiClient{}
	client.serial.Store(0x12345678)
	sender := &eiSender{client: client, keyboard: 31, keyboardDev: 32, button: 41, buttonDev: 42}
	for _, tc := range []struct {
		name           string
		send           func() ([]byte, error)
		object, device uint64
		code, state    uint32
	}{
		{"key press", func() ([]byte, error) { return eiKeyFrame(sender, 30, true) }, 31, 32, 30, 1},
		{"key release", func() ([]byte, error) { return eiKeyFrame(sender, 30, false) }, 31, 32, 30, 0},
		{"button press", func() ([]byte, error) { return eiButtonFrame(sender, 0x110, true) }, 41, 42, 0x110, 1},
		{"button release", func() ([]byte, error) { return eiButtonFrame(sender, 0x110, false) }, 41, 42, 0x110, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := tc.send()
			if err != nil {
				t.Fatal(err)
			}
			event, rest := splitEIRequest(t, data)
			if event.object != tc.object || event.opcode != 1 || len(event.body) != 8 {
				t.Fatalf("wrong event header %+v", event)
			}
			if key, state := binary.NativeEndian.Uint32(event.body[:4]), binary.NativeEndian.Uint32(event.body[4:]); key != tc.code || state != tc.state {
				t.Fatalf("wire state=%d/%d want %d/%d", key, state, tc.code, tc.state)
			}
			frame, end := splitEIRequest(t, rest)
			if len(end) != 0 || frame.object != tc.device || frame.opcode != 3 || len(frame.body) != 12 {
				t.Fatalf("wrong device frame %+v trailing=%d", frame, len(end))
			}
			if serial := binary.NativeEndian.Uint32(frame.body[:4]); serial != 0x12345678 {
				t.Fatalf("unexpected frame serial: %x", serial)
			}
		})
	}
}

func TestEIFrameRejectsMissingAuthorizedDevicesWithoutWireEvents(t *testing.T) {
	client := &eiClient{}
	for _, tc := range []struct {
		name string
		send func() ([]byte, error)
	}{
		{"nil key sender", func() ([]byte, error) { return eiKeyFrame(nil, 30, true) }},
		{"missing keyboard", func() ([]byte, error) { return eiKeyFrame(&eiSender{client: client, keyboardDev: 32}, 30, true) }},
		{"missing keyboard device", func() ([]byte, error) { return eiKeyFrame(&eiSender{client: client, keyboard: 31}, 30, true) }},
		{"nil button sender", func() ([]byte, error) { return eiButtonFrame(nil, 0x110, true) }},
		{"missing button", func() ([]byte, error) { return eiButtonFrame(&eiSender{client: client, buttonDev: 42}, 0x110, true) }},
		{"missing button device", func() ([]byte, error) { return eiButtonFrame(&eiSender{client: client, button: 41}, 0x110, true) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire, err := tc.send()
			if !errors.Is(err, ErrNotSupported) || len(wire) != 0 {
				t.Fatalf("unowned device emitted %d wire bytes: %v", len(wire), err)
			}
		})
	}
}

func TestEIEmptyLengthStringAndNegativeOffsetRemainBounded(t *testing.T) {
	if value, err := eiReadString(eiU32(0), 0); err != nil || value != "" {
		t.Fatalf("zero-length string=%q err=%v", value, err)
	}
	if value, err := eiReadString(eiU32(0), -1); err == nil || value != "" {
		t.Fatalf("negative string offset accepted: %q %v", value, err)
	}
}
