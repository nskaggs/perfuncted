//go:build linux
// +build linux

package input

import (
	"slices"
	"testing"
)

func TestGnomeKeyval(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want uint32
	}{
		{key: "a", want: 'a'},
		{key: "A", want: 'a'},
		{key: "space", want: 0x20},
		{key: "return", want: 0xff0d},
		{key: "ctrl", want: 0xffe3},
		{key: "f12", want: 0xffc9},
		{key: "é", want: 0x00e9},
	} {
		t.Run(tc.key, func(t *testing.T) {
			got, err := gnomeKeyval(tc.key)
			if err != nil {
				t.Fatalf("gnomeKeyval(%q): %v", tc.key, err)
			}
			if got != tc.want {
				t.Fatalf("gnomeKeyval(%q) = %#x, want %#x", tc.key, got, tc.want)
			}
		})
	}
	if _, err := gnomeKeyval("unknown-key"); err == nil {
		t.Fatal("gnomeKeyval(unknown-key) succeeded")
	}
}

func TestGnomeNativeBackendDoesNotAdvertiseSync(t *testing.T) {
	if slices.Contains((&GnomeNativeBackend{}).SupportedOperations(), "sync") {
		t.Fatal("GNOME native input must not advertise unsupported synchronization")
	}
}
