//go:build linux

package input

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
)

type pointerGnomeBridge struct {
	recordingGnomeBridge
	events                        []string
	moveErr, buttonErr, scrollErr error
	cancelOnPress                 context.CancelFunc
}

func (b *pointerGnomeBridge) PointerMove(_ context.Context, x, y int32) error {
	b.events = append(b.events, fmt.Sprintf("move:%d,%d", x, y))
	return b.moveErr
}
func (b *pointerGnomeBridge) PointerButton(_ context.Context, button uint32, pressed bool) error {
	state := "up"
	if pressed {
		state = "down"
	}
	b.events = append(b.events, fmt.Sprintf("%s:%d", state, button))
	if pressed && b.cancelOnPress != nil {
		b.cancelOnPress()
	}
	return b.buttonErr
}
func (b *pointerGnomeBridge) Scroll(_ context.Context, axis string, amount float64) error {
	b.events = append(b.events, fmt.Sprintf("scroll:%s:%g", axis, amount))
	return b.scrollErr
}

func TestGnomePointerNormalClickAndFourScrollDirections(t *testing.T) {
	bridge := &pointerGnomeBridge{}
	b := &GnomeNativeBackend{bridge: bridge}
	if err := b.MouseClick(context.Background(), 234, 117, 1); err != nil {
		t.Fatal(err)
	}
	want := "move:234,117,down:1,up:1"
	if got := strings.Join(bridge.events, ","); got != want {
		t.Fatalf("click events=%q want %q", got, want)
	}
	for _, tc := range []struct {
		name string
		run  func() error
		want string
	}{
		{"up", func() error { return b.ScrollUp(context.Background(), 3) }, "scroll:vertical:-3"},
		{"down", func() error { return b.ScrollDown(context.Background(), 2) }, "scroll:vertical:2"},
		{"left", func() error { return b.ScrollLeft(context.Background(), 4) }, "scroll:horizontal:-4"},
		{"right", func() error { return b.ScrollRight(context.Background(), 1) }, "scroll:horizontal:1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); err != nil {
				t.Fatal(err)
			}
			if got := bridge.events[len(bridge.events)-1]; got != tc.want {
				t.Errorf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestGnomePointerFailuresDoNotLeaveButtonHeld(t *testing.T) {
	for _, tc := range []struct {
		name       string
		setup      func(*pointerGnomeBridge, context.CancelFunc)
		wantEvents string
	}{
		{"move", func(b *pointerGnomeBridge, _ context.CancelFunc) { b.moveErr = errors.New("move failed") }, "move:12,13"},
		{"cancel-after-down", func(b *pointerGnomeBridge, c context.CancelFunc) { b.cancelOnPress = c }, "move:12,13,down:1,up:1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			bridge := &pointerGnomeBridge{}
			tc.setup(bridge, cancel)
			b := &GnomeNativeBackend{bridge: bridge}
			err := b.MouseClick(ctx, 12, 13, 1)
			if err == nil {
				t.Fatal("expected injected failure")
			}
			if got := strings.Join(bridge.events, ","); got != tc.wantEvents {
				t.Errorf("unexpected event sequence %s", got)
			}
		})
	}
}

func TestGnomePointerRejectsBadCoordinatesAndButtonsBeforeEmitting(t *testing.T) {
	bridge := &pointerGnomeBridge{}
	b := &GnomeNativeBackend{bridge: bridge}
	for _, xy := range [][2]int{{math.MaxInt, 0}, {0, math.MinInt}} {
		if err := b.MouseMove(context.Background(), xy[0], xy[1]); err == nil {
			t.Errorf("move %v unexpectedly accepted", xy)
		}
	}
	for _, button := range []int{-1, 0, 4, 255} {
		if err := b.MouseClick(context.Background(), 10, 20, button); err == nil {
			t.Errorf("click %d accepted", button)
		}
		if err := b.MouseDown(context.Background(), button); err == nil {
			t.Errorf("down %d accepted", button)
		}
	}
	if len(bridge.events) != 0 {
		t.Errorf("invalid operations emitted: %v", bridge.events)
	}
}

func TestGnomeScrollZeroAndInvalidDoNotEmit(t *testing.T) {
	bridge := &pointerGnomeBridge{}
	b := &GnomeNativeBackend{bridge: bridge}
	for _, n := range []int{-1, math.MaxInt} {
		if err := b.ScrollDown(context.Background(), n); err == nil {
			t.Errorf("scroll %d accepted", n)
		}
	}
	if err := b.ScrollDown(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if len(bridge.events) != 0 {
		t.Errorf("unexpected scroll events %v", bridge.events)
	}
}
