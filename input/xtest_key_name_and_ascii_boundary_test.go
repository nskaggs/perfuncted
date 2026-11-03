//go:build linux

package input

import (
	"strings"
	"testing"

	"github.com/jezek/xgb/xproto"
)

func TestXTestKeyNameResolutionPrintableASCIIAndUntrustedBytes(t *testing.T) {
	backend := &XTestBackend{keymap: map[xproto.Keysym]keycodeLevel{
		xproto.Keysym('~'):    {keycode: 47},
		xproto.Keysym('a'):    {keycode: 38},
		keysymForName["ctrl"]: {keycode: 37},
	}}
	for _, tc := range []struct {
		name string
		want xproto.Keycode
	}{
		{"~", 47}, {"a", 38}, {"ctrl", 37},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := backend.keycodeFor(tc.name)
			if err != nil || got != tc.want {
				t.Fatalf("key=%q got=%d error=%v, want=%d", tc.name, got, err, tc.want)
			}
		})
	}
	for _, key := range []string{"", "unrecognized-key", "\x00", "\x1f", "\x7f", "\xff"} {
		t.Run("invalid-"+key, func(t *testing.T) {
			got, err := backend.keycodeFor(key)
			if got != 0 || err == nil || !strings.Contains(err.Error(), "unknown key") {
				t.Fatalf("unsupported key=%q accepted as keycode=%d error=%v", key, got, err)
			}
		})
	}
	missing := &XTestBackend{keymap: map[xproto.Keysym]keycodeLevel{}}
	if got, err := missing.keycodeFor("~"); got != 0 || err == nil || !strings.Contains(err.Error(), "not found in keymap") {
		t.Fatalf("unmapped printable key=%d err=%v", got, err)
	}
}
