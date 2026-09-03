//go:build linux
// +build linux

package input

import (
	"context"
	"errors"
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

func TestGnomeDirectTextOnlyUsesLayoutSafeCharacters(t *testing.T) {
	for _, test := range []struct {
		text string
		want bool
	}{
		{text: "hello, world!\n", want: true},
		{text: "", want: true},
		{text: "é", want: false},
		{text: "\u0001", want: false},
	} {
		if got := gnomeDirectText(test.text); got != test.want {
			t.Fatalf("gnomeDirectText(%q) = %v, want %v", test.text, got, test.want)
		}
	}
}

func TestUpdateHeldModifierTracksExplicitModifierActions(t *testing.T) {
	var held modifiers
	for _, test := range []struct {
		key  string
		down bool
		want modifiers
	}{
		{key: "ctrl", down: true, want: modifiers{ctrl: true}},
		{key: "control", down: false, want: modifiers{}},
		{key: "meta", down: true, want: modifiers{super: true}},
		{key: "super", down: false, want: modifiers{}},
	} {
		updateHeldModifier(&held, test.key, test.down)
		if held != test.want {
			t.Fatalf("updateHeldModifier(%q, %v) = %#v, want %#v", test.key, test.down, held, test.want)
		}
	}
}

func TestReleaseMouseButtonUsesIndependentCleanupContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var releaseCtxErr error
	err := releaseMouseButton(ctx, func(ctx context.Context) error {
		releaseCtxErr = ctx.Err()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("releaseMouseButton error = %v, want context.Canceled", err)
	}
	if releaseCtxErr != nil {
		t.Fatalf("release context error = %v, want nil during cleanup", releaseCtxErr)
	}
}

func TestReleaseMouseButtonJoinsReleaseError(t *testing.T) {
	releaseErr := errors.New("release failed")
	err := releaseMouseButton(context.Background(), func(context.Context) error {
		return releaseErr
	})
	if !errors.Is(err, releaseErr) {
		t.Fatalf("releaseMouseButton error = %v, want release error", err)
	}
}
