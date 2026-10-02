package input

import (
	"slices"
	"testing"
)

// A backend may only advertise sync when it can actually observe the compositor
// acting on injected events. Reporting success without a barrier hands callers a
// postcondition that is not established, which is how a follow-up screen read
// observes state from before the keystrokes.
func TestSyncIsAdvertisedOnlyByBackendsWithACompletionBarrier(t *testing.T) {
	backends := []struct {
		name    string
		backend interface{ SupportedOperations() []string }
		want    bool
	}{
		{name: "xtest", backend: &XTestBackend{}, want: true},
		{name: "wl-virtual", backend: &WlVirtualBackend{}, want: false},
		{name: "uinput", backend: &UinputBackend{}, want: false},
		{name: "portal", backend: &RemoteDesktopBackend{}, want: false},
		{name: "gnome-native", backend: &GnomeNativeBackend{}, want: false},
	}
	for _, tc := range backends {
		t.Run(tc.name, func(t *testing.T) {
			advertised := slices.Contains(tc.backend.SupportedOperations(), "sync")
			if advertised != tc.want {
				t.Fatalf("sync advertised = %t, want %t (operations %v)",
					advertised, tc.want, tc.backend.SupportedOperations())
			}
		})
	}
}

// Advertised sync and an implemented barrier must agree: a backend that claims
// the operation has to satisfy Syncer, and one that does not claim it must not
// pretend to provide one.
func TestSyncAdvertisementMatchesImplementedBarrier(t *testing.T) {
	backends := []struct {
		name    string
		backend interface {
			Inputter
			SupportedOperations() []string
		}
	}{
		{name: "xtest", backend: &XTestBackend{}},
		{name: "wl-virtual", backend: &WlVirtualBackend{}},
		{name: "uinput", backend: &UinputBackend{}},
		{name: "portal", backend: &RemoteDesktopBackend{}},
		{name: "gnome-native", backend: &GnomeNativeBackend{}},
	}
	for _, tc := range backends {
		t.Run(tc.name, func(t *testing.T) {
			_, isSyncer := any(tc.backend).(Syncer)
			advertised := slices.Contains(tc.backend.SupportedOperations(), "sync")
			if isSyncer != advertised {
				t.Fatalf("implements Syncer = %t but advertised sync = %t; a backend must not claim a barrier it cannot provide",
					isSyncer, advertised)
			}
		})
	}
}
