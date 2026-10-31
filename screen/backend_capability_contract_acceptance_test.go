package screen

import (
	"reflect"
	"slices"
	"testing"

	"github.com/nskaggs/perfuncted/internal/capability"
)

func TestSyntheticScreenBackendsAdvertiseConsistentCapabilities(t *testing.T) {
	want := capability.Operations("screen")
	backends := []struct {
		name      string
		available func() []string
	}{
		{"x11", func() []string { return (*X11Backend)(nil).SupportedOperations() }},
		{"kwin", func() []string { return (*KWinShotBackend)(nil).SupportedOperations() }},
		{"gnome-shell", func() []string { return (*GnomeShellScreenshotBackend)(nil).SupportedOperations() }},
		{"gnome-native", func() []string { return (*GnomeNativeScreenBackend)(nil).SupportedOperations() }},
		{"external-capture", func() []string { return (*ExtCaptureBackend)(nil).SupportedOperations() }},
		{"wlroots", func() []string { return (*WlrScreencopyBackend)(nil).SupportedOperations() }},
	}
	for _, backend := range backends {
		t.Run(backend.name, func(t *testing.T) {
			got := backend.available()
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("advertised %v, expected %v", got, want)
			}
			if len(got) == 0 {
				t.Fatal("empty capability contract")
			}
			got[0] = "tampered"
			if !reflect.DeepEqual(backend.available(), want) {
				t.Fatal("client could mutate capability registry")
			}
		})
	}
	portal := (*PortalDBusBackend)(nil)
	expected := capability.Operations("screen", "wait", "wait-change", "wait-stable")
	actual := portal.SupportedOperations()
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("portal advertised %v, expected %v", actual, expected)
	}
	if slices.Contains(actual, "wait-change") || slices.Contains(actual, "wait-stable") {
		t.Fatalf("expensive portal wait advertised: %v", actual)
	}
}

func TestScreenPixelHashImplementationsAdvertiseCanonicalSemantics(t *testing.T) {
	for name, backend := range map[string]interface{ CanonicalHashing() bool }{
		"portal":      (*PortalDBusBackend)(nil),
		"gnome-shell": (*GnomeShellScreenshotBackend)(nil),
		"wlroots":     (*WlrScreencopyBackend)(nil),
	} {
		if !backend.CanonicalHashing() {
			t.Errorf("%s did not advertise pixel hash equivalence", name)
		}
	}
}
