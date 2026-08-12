//go:build linux
// +build linux

package input

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

type xtestEvent struct {
	eventType byte
	detail    byte
}

func newTestXTestBackend(
	t *testing.T,
	keysymsPerKeycode byte,
	keysyms []xproto.Keysym,
	fail func(xtestEvent) error,
) (*XTestBackend, *[]xtestEvent) {
	t.Helper()
	if keysymsPerKeycode == 0 || len(keysyms)%int(keysymsPerKeycode) != 0 {
		t.Fatalf("invalid test keymap: %d keysyms per keycode, %d keysyms", keysymsPerKeycode, len(keysyms))
	}

	events := make([]xtestEvent, 0, 8)
	conn := &x11.MockConnection{
		SetupFunc: func() *xproto.SetupInfo {
			return &xproto.SetupInfo{
				MinKeycode: 8,
				MaxKeycode: 8 + xproto.Keycode(len(keysyms)/int(keysymsPerKeycode)) - 1,
			}
		},
		GetKeyboardMappingFunc: func(xproto.Keycode, byte) x11.GetKeyboardMappingCookie {
			return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{
				KeysymsPerKeycode: keysymsPerKeycode,
				Keysyms:           keysyms,
			})
		},
		FakeInputCheckedFunc: func(eventType byte, detail byte, _ uint32, _ xproto.Window, _, _ int16, _ byte) x11.XTestFakeInputCookie {
			event := xtestEvent{eventType: eventType, detail: detail}
			events = append(events, event)
			if fail != nil {
				if err := fail(event); err != nil {
					return x11.NewMockXTestFakeInputCookie(err)
				}
			}
			return x11.NewMockXTestFakeInputCookie(nil)
		},
	}
	return &XTestBackend{conn: conn, root: 1}, &events
}

func TestXTestTypeReleasesTemporaryModifiersAfterKeyFailure(t *testing.T) {
	const (
		shift = byte(8)
		ctrl  = byte(9)
		key   = byte(12)
	)
	wantErr := errors.New("synthetic key failure")
	b, events := newTestXTestBackend(t, 1, []xproto.Keysym{
		0xffe1, // shift
		0xffe3, // ctrl
		0xffe9, // alt
		0xffeb, // super
		0x61,   // a
	}, func(event xtestEvent) error {
		if event.eventType == xproto.KeyPress && event.detail == key {
			return wantErr
		}
		return nil
	})

	err := b.typeContext(context.Background(), "{ctrl+shift+a}")
	if !errors.Is(err, wantErr) {
		t.Fatalf("typeContext error = %v, want %v", err, wantErr)
	}

	want := []xtestEvent{
		{eventType: xproto.KeyPress, detail: shift},
		{eventType: xproto.KeyPress, detail: ctrl},
		{eventType: xproto.KeyPress, detail: key},
		{eventType: xproto.KeyRelease, detail: ctrl},
		{eventType: xproto.KeyRelease, detail: shift},
	}
	if !sameXTestEvents(*events, want) {
		t.Fatalf("events = %#v, want %#v", *events, want)
	}
}

func TestXTestTypeReleasesEarlierModifiersWhenModifierSetupFails(t *testing.T) {
	const (
		ctrl  = byte(9)
		shift = byte(8)
	)
	wantErr := errors.New("synthetic modifier failure")
	b, events := newTestXTestBackend(t, 1, []xproto.Keysym{
		0xffe1, // shift
		0xffe3, // ctrl
		0xffe9, // alt
		0xffeb, // super
		0x61,   // a
	}, func(event xtestEvent) error {
		if event.eventType == xproto.KeyPress && event.detail == ctrl {
			return wantErr
		}
		return nil
	})

	err := b.typeContext(context.Background(), "{ctrl+shift+a}")
	if !errors.Is(err, wantErr) {
		t.Fatalf("typeContext error = %v, want %v", err, wantErr)
	}

	want := []xtestEvent{
		{eventType: xproto.KeyPress, detail: shift},
		{eventType: xproto.KeyPress, detail: ctrl},
		{eventType: xproto.KeyRelease, detail: shift},
	}
	if !sameXTestEvents(*events, want) {
		t.Fatalf("events = %#v, want %#v", *events, want)
	}
}

func TestXTestTypeTextReleasesShiftAfterKeyFailure(t *testing.T) {
	const (
		shift = byte(8)
		key   = byte(9)
	)
	wantErr := errors.New("synthetic text key failure")
	b, events := newTestXTestBackend(t, 2, []xproto.Keysym{
		0xffe1, 0,
		0x61, 0x41,
	}, func(event xtestEvent) error {
		if event.eventType == xproto.KeyPress && event.detail == key {
			return wantErr
		}
		return nil
	})

	err := b.typeContext(context.Background(), "A")
	if !errors.Is(err, wantErr) {
		t.Fatalf("typeContext error = %v, want %v", err, wantErr)
	}

	want := []xtestEvent{
		{eventType: xproto.KeyPress, detail: shift},
		{eventType: xproto.KeyPress, detail: key},
		{eventType: xproto.KeyRelease, detail: shift},
	}
	if !sameXTestEvents(*events, want) {
		t.Fatalf("events = %#v, want %#v", *events, want)
	}
}

func TestXTestTypeReleasesTemporaryModifiersAfterCancellation(t *testing.T) {
	const (
		shift = byte(8)
		ctrl  = byte(9)
		key   = byte(12)
	)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b, events := newTestXTestBackend(t, 1, []xproto.Keysym{
		0xffe1, // shift
		0xffe3, // ctrl
		0xffe9, // alt
		0xffeb, // super
		0x61,   // a
	}, func(event xtestEvent) error {
		if event.eventType == xproto.KeyPress && event.detail == key {
			cancel()
		}
		return nil
	})
	b.delay = 10 * time.Millisecond

	err := b.typeContext(ctx, "{ctrl+shift+a}")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("typeContext error = %v, want context.Canceled", err)
	}

	want := []xtestEvent{
		{eventType: xproto.KeyPress, detail: shift},
		{eventType: xproto.KeyPress, detail: ctrl},
		{eventType: xproto.KeyPress, detail: key},
		{eventType: xproto.KeyRelease, detail: key},
		{eventType: xproto.KeyRelease, detail: ctrl},
		{eventType: xproto.KeyRelease, detail: shift},
	}
	if !sameXTestEvents(*events, want) {
		t.Fatalf("events = %#v, want %#v", *events, want)
	}
}

func sameXTestEvents(got, want []xtestEvent) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
