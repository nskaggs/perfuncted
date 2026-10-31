//go:build linux

package input

import (
	"context"
	"errors"
	"testing"

	"github.com/bendahl/uinput"
)

// Middle-click needs a separately acquired uinput mouse. A failed lazy
// creation must not move the pointer before reporting an unavailable button.
func TestUinputMiddleClickDoesNotMoveBeforeMouseDeviceAcquisition(t *testing.T) {
	b, tp, _ := newUinputActionBackend()
	b.mouse = nil
	unavailable := errors.New("synthetic /dev/uinput denied")
	old := createUinputMouse
	createUinputMouse = func(string, []byte) (uinput.Mouse, error) { return nil, unavailable }
	t.Cleanup(func() { createUinputMouse = old })
	err := b.MouseClick(context.Background(), 30, 40, 2)
	if !errors.Is(err, unavailable) {
		t.Fatalf("unavailable middle mouse returned %v", err)
	}
	if len(tp.events) != 0 {
		t.Fatalf("click moved pointer before discovering unavailable middle-button device: %v", tp.events)
	}
}

func TestUinputMiddleClickWithAcquiredMousePreservesMotionPressRelease(t *testing.T) {
	b, tp, mouse := newUinputActionBackend()
	if err := b.MouseClick(context.Background(), 30, 40, 2); err != nil {
		t.Fatal(err)
	}
	if got := tp.events; len(got) != 1 || got[0] != "move:30,40" {
		t.Fatalf("middle click motion=%v", got)
	}
	if got := mouse.eventsSnapshot(); len(got) != 2 || got[0] != "middle-press" || got[1] != "middle-release" {
		t.Fatalf("middle click events=%v", got)
	}
}
