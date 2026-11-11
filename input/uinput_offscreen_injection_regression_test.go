//go:build linux

package input

import (
	"context"
	"os"
	"reflect"
	"testing"

	"github.com/bendahl/uinput"
)

// KWin clamps out-of-range virtual touchpad coordinates without reporting an
// injection error. Reject them before issuing any events or clicking an edge.
func TestUinputRejectsOffscreenMoveAndClickWithoutEvents(t *testing.T) {
	touchpad := &recordingTouchPad{}
	withUinputSyntheticConstructors(t,
		func(string) (os.FileInfo, error) { return nil, nil },
		func(string, []byte) (uinput.Keyboard, error) { return &constructorRecordingKeyboard{}, nil },
		func(string, []byte, int32, int32, int32, int32) (uinput.TouchPad, error) { return touchpad, nil },
		func() (map[rune]kernelChar, error) { return qwertyRuneMap(), nil },
	)
	b, err := NewUinputBackend(2867, 1200)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	invalid := []struct {
		name string
		x, y int
	}{
		{"negative X", -1, 600}, {"negative Y", 1400, -1},
		{"first invalid X", 2867, 600}, {"first invalid Y", 1400, 1200},
		{"large negative X", -999, 600}, {"large positive X", 99999, 600},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if err := b.MouseMove(ctx, tc.x, tc.y); err == nil {
				t.Errorf("offscreen move %d,%d was accepted", tc.x, tc.y)
			}
			if err := b.MouseClick(ctx, tc.x, tc.y, 1); err == nil {
				t.Errorf("offscreen click %d,%d was accepted", tc.x, tc.y)
			}
			if len(touchpad.events) != 0 {
				t.Fatalf("offscreen input injected events: %v", touchpad.events)
			}
		})
	}
	for _, tc := range []struct{ x, y int }{{0, 0}, {2866, 0}, {2866, 1199}, {0, 1199}} {
		if err := b.MouseMove(ctx, tc.x, tc.y); err != nil {
			t.Fatalf("valid edge %d,%d rejected: %v", tc.x, tc.y, err)
		}
	}
	if want := []string{"move:0,0", "move:2866,0", "move:2866,1199", "move:0,1199"}; !reflect.DeepEqual(touchpad.events, want) {
		t.Fatalf("valid edge events = %v, want %v", touchpad.events, want)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
}
