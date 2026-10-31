package x11

import (
	"github.com/jezek/xgb/xproto"
	"testing"
)

func TestX11MockCookieConstructorsPreserveRealProtocolReplies(t *testing.T) {
	keys := &xproto.GetKeyboardMappingReply{KeysymsPerKeycode: 2, Keysyms: []xproto.Keysym{97, 65}}
	keyboard := NewMockGetKeyboardMappingCookie(keys)
	got, err := keyboard.Reply()
	if err != nil || got != keys || len(got.Keysyms) != 2 || got.Keysyms[1] != 65 {
		t.Fatalf("X11 keyboard cookie lost keymap: %+v %v", got, err)
	}
	property := &xproto.GetPropertyReply{Format: 8, Value: []byte("private-window-title")}
	get := NewMockGetPropertyCookie(property)
	value, err := get.Reply()
	if err != nil || value != property || string(value.Value) != "private-window-title" {
		t.Fatalf("X11 property cookie lost exact bytes: %+v %v", value, err)
	}
}

func TestNewXgbConnectionRejectsPrivateInvalidDisplayWithoutHostFallback(t *testing.T) {
	// An impossible numeric display is explicit; never consult $DISPLAY or
	// XAUTHORITY, never touch a real desktop and never retry a host connection.
	conn, err := NewXgbConnection(":-1")
	if conn != nil {
		conn.Close()
		t.Fatal("invalid isolated X display unexpectedly connected")
	}
	if err == nil {
		t.Fatal("invalid isolated X display returned no error")
	}
}
