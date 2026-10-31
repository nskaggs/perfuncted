//go:build linux

package input

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/bendahl/uinput"
)

// The synthetic uinput devices provide real evidence of events at the evdev
// interface boundary; horizontal and vertical scroll directions cannot be
// validated by merely checking for a nil error.
func TestUinputAllPointerButtonsAndScrollDirectionsProduceExpectedDeviceEvents(t *testing.T) {
	b, tp, mouse := newUinputActionBackend()
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		call  func() error
		tp    []string
		mouse []string
	}{
		{"left button down", func() error { return b.MouseDown(ctx, 1) }, []string{"left-press"}, nil},
		{"left button up", func() error { return b.MouseUp(ctx, 1) }, []string{"left-release"}, nil},
		{"right button down", func() error { return b.MouseDown(ctx, 3) }, []string{"right-press"}, nil},
		{"right button up", func() error { return b.MouseUp(ctx, 3) }, []string{"right-release"}, nil},
		{"middle button down", func() error { return b.MouseDown(ctx, 2) }, nil, []string{"middle-press"}},
		{"middle button up", func() error { return b.MouseUp(ctx, 2) }, nil, []string{"middle-release"}},
		{"logical move", func() error { return b.MouseMove(ctx, 15, 42) }, []string{"move:15,42"}, nil},
		{"scroll up", func() error { return b.ScrollUp(ctx, 3) }, nil, []string{"wheel:false:-3"}},
		{"scroll down", func() error { return b.ScrollDown(ctx, 2) }, nil, []string{"wheel:false:2"}},
		{"scroll left", func() error { return b.ScrollLeft(ctx, 5) }, nil, []string{"wheel:true:-5"}},
		{"scroll right", func() error { return b.ScrollRight(ctx, 7) }, nil, []string{"wheel:true:7"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			beforeTouch, beforeMouse := len(tp.events), len(mouse.eventsSnapshot())
			if err := tc.call(); err != nil {
				t.Fatal(err)
			}
			if actual := tp.events[beforeTouch:]; !slices.Equal(actual, tc.tp) {
				t.Fatalf("touchpad emitted %v, want %v", actual, tc.tp)
			}
			if actual := mouse.eventsSnapshot()[beforeMouse:]; !slices.Equal(actual, tc.mouse) {
				t.Fatalf("mouse emitted %v, want %v", actual, tc.mouse)
			}
		})
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if got := mouse.eventsSnapshot(); got[len(got)-1] != "close" {
		t.Fatalf("mouse was not closed: %v", got)
	}
}

func TestUinputExplicitModifierAndLiteralTypingPreserveKeyboardState(t *testing.T) {
	b, kb := newTestBackend(t)
	ctx := context.Background()
	if err := b.Type(ctx, "{super down}{super+a}{super up}"); err != nil {
		t.Fatal(err)
	}
	if b.modifierHeld(uinput.KeyLeftmeta) {
		t.Fatal("held super not released by explicit up")
	}
	want := []string{fmt.Sprintf("down:%d", uinput.KeyLeftmeta), fmt.Sprintf("press:%d", uinput.KeyA), fmt.Sprintf("up:%d", uinput.KeyLeftmeta)}
	if !slices.Equal(kb.events, want) {
		t.Fatalf("modifier and key wire order %v, want %v", kb.events, want)
	}
	if err := b.TypeLiteral(ctx, "Aa"); err != nil {
		t.Fatal(err)
	}
	tail := kb.events[len(want):]
	literalWant := []string{fmt.Sprintf("down:%d", uinput.KeyLeftshift), fmt.Sprintf("press:%d", uinput.KeyA), fmt.Sprintf("up:%d", uinput.KeyLeftshift), fmt.Sprintf("press:%d", uinput.KeyA)}
	if !slices.Equal(tail, literalWant) {
		t.Fatalf("literal text event order %v, want %v", tail, literalWant)
	}
}

func TestUinputUnavailablePointerDoesNotReportLocation(t *testing.T) {
	b, _, _ := newUinputActionBackend()
	x, y, err := b.PointerLocation(context.Background())
	if x != 0 || y != 0 || !errors.Is(err, ErrNotSupported) {
		t.Fatalf("unavailable pointer reported %d,%d %v", x, y, err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, err = b.PointerLocation(context.Background())
	if !errors.Is(err, errUinputBackendClosed) {
		t.Fatalf("closed pointer backend error %v", err)
	}
}
