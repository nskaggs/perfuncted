package input

import (
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf8"
)

// A multi-frame text transaction is valid only if each encoded EIS text
// argument contains complete UTF-8 code points, each frame targets the
// negotiated text device and every frame retains the negotiated serial.
func TestEISUnicodeTextMultipleFramesKeepCompleteRunesAndSerial(t *testing.T) {
	client := &eiClient{}
	client.serial.Store(0x7fa11234)
	sender := &eiSender{client: client, text: eiServerIDFloor + 31, textDev: eiServerIDFloor + 32}
	for _, tc := range []struct {
		name, content string
		minFrames     int
	}{
		{"four-byte boundary", strings.Repeat("a", 253) + "🛶" + strings.Repeat("€", 95), 2},
		{"many multibyte frames", strings.Repeat("🛶éç漢", 200), 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frames, err := eiTextFrames(sender, tc.content)
			if err != nil || len(frames) < tc.minFrames {
				t.Fatalf("text frame count=%d err=%v", len(frames), err)
			}
			var restored strings.Builder
			for i, wire := range frames {
				request, remaining := splitEIRequest(t, wire)
				if request.object != sender.text || request.opcode != 2 {
					t.Fatalf("frame %d text event=%+v", i, request)
				}
				chunk, err := eiReadString(request.body, 0)
				if err != nil || len(chunk) == 0 || len(chunk) > 254 || !utf8.ValidString(chunk) {
					t.Fatalf("frame %d chunk bytes=%d err=%v", i, len(chunk), err)
				}
				restored.WriteString(chunk)
				device, trailing := splitEIRequest(t, remaining)
				if len(trailing) != 0 || device.object != sender.textDev || device.opcode != 3 || len(device.body) != 12 {
					t.Fatalf("frame %d device=%+v trailing=%d", i, device, len(trailing))
				}
				if serial := binary.NativeEndian.Uint32(device.body[:4]); serial != 0x7fa11234 {
					t.Fatalf("frame %d serial=%08x", i, serial)
				}
			}
			if restored.String() != tc.content {
				t.Fatal("multi-frame text changed codepoints or ordering")
			}
		})
	}
}
