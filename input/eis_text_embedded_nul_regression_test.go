package input

import (
	"strings"
	"testing"
)

// EIS strings are NUL-terminated and cannot contain a second terminator
// inside their body. Reject the entire input before emitting any text frame.
func TestEISTextFramesRejectEmbeddedNULWithoutPartialOutput(t *testing.T) {
	sender := &eiSender{client: &eiClient{}, text: 202, textDev: 201}
	for _, text := range []string{"\x00", "prefix\x00suffix", strings.Repeat("a", 260) + "\x00tail"} {
		frames, err := eiTextFrames(sender, text)
		if err == nil || len(frames) != 0 {
			t.Errorf("accepted embedded NUL in %q: frames=%d err=%v", text, len(frames), err)
		}
	}
	frames, err := eiTextFrames(sender, "valid text")
	if err != nil || len(frames) != 1 {
		t.Fatalf("valid text rejected: frames=%d err=%v", len(frames), err)
	}
}
