//go:build linux

package input

import (
	"os"
	"testing"

	"github.com/bendahl/uinput"
)

// A pointer device covering W logical pixels must expose axis positions 0..W-1.
// Advertising 0..W makes KWin map the requested center and right half to the
// previous pixel, turning verified relative moves into false failures.
func TestUinputTouchpadAxesUseLastLogicalPixel(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int32
	}{
		{name: "normal", width: 1600, height: 900},
		{name: "fractional-scaled-real-kde", width: 2867, height: 1200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withUinputSyntheticConstructors(t,
				func(string) (os.FileInfo, error) { return nil, nil },
				func(string, []byte) (uinput.Keyboard, error) { return &constructorRecordingKeyboard{}, nil },
				func(_ string, _ []byte, minX, maxX, minY, maxY int32) (uinput.TouchPad, error) {
					if minX != 0 || minY != 0 || maxX != tc.width-1 || maxY != tc.height-1 {
						t.Fatalf("touchpad bounds (%d..%d,%d..%d) do not match last logical pixel (%d,%d)",
							minX, maxX, minY, maxY, tc.width-1, tc.height-1)
					}
					return &recordingTouchPad{}, nil
				},
				func() (map[rune]kernelChar, error) { return qwertyRuneMap(), nil },
			)
			b, err := NewUinputBackend(tc.width, tc.height)
			if err != nil {
				t.Fatal(err)
			}
			if err := b.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
