package input

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"testing"
)

// Session capability discovery must reflect actual EIS objects. Advertising
// click when only pointer motion was granted leads callers to assume a
// permission they never obtained and to send protocol object-zero frames.
func TestRemoteDesktopAdvertisesOnlyActuallyBoundEISCapabilities(t *testing.T) {
	if ops := (&RemoteDesktopBackend{}).SupportedOperations(); len(ops) != 0 {
		t.Errorf("incomplete portal advertised nonexistent operations: %v", ops)
	}
	for _, tc := range []struct {
		name    string
		disable func(*eiSender)
		absent  []string
	}{
		{"keyboard object unavailable", func(s *eiSender) { s.keyboard = 0 }, []string{"keyboard"}},
		{"keyboard device unavailable", func(s *eiSender) { s.keyboardDev = 0 }, []string{"keyboard"}},
		{"button object unavailable", func(s *eiSender) { s.button = 0 }, []string{"click", "drag"}},
		{"button device unavailable", func(s *eiSender) { s.buttonDev = 0 }, []string{"click", "drag"}},
		{"pointer object unavailable", func(s *eiSender) { s.pointers[0].pointer = 0 }, []string{"pointer", "click", "drag", "pointer-coordinate-space"}},
		{"pointer device unavailable", func(s *eiSender) { s.pointers[0].device = 0 }, []string{"pointer", "click", "drag", "pointer-coordinate-space"}},
		{"zero-width pointer region", func(s *eiSender) { s.pointers[0].regions[0].width = 0 }, []string{"pointer", "click", "drag", "pointer-coordinate-space"}},
		{"zero-height pointer region", func(s *eiSender) { s.pointers[0].regions[0].height = 0 }, []string{"pointer", "click", "drag", "pointer-coordinate-space"}},
		{"scroll object unavailable", func(s *eiSender) { s.scroll = 0 }, []string{"scroll"}},
		{"scroll device unavailable", func(s *eiSender) { s.scrollDev = 0 }, []string{"scroll"}},
		{"fully authorized", func(*eiSender) {}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _, _ := newSyntheticRevocableRemoteDesktop(t)
			b.sender.pointers = []eiPointerDevice{{device: 501, pointer: 502, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}}}
			tc.disable(b.sender)
			ops := b.SupportedOperations()
			for _, name := range tc.absent {
				if slices.Contains(ops, name) {
					t.Errorf("missing EIS authorization advertised %q: %v", name, ops)
				}
			}
			if tc.name == "fully authorized" {
				for _, name := range []string{"keyboard", "pointer", "click", "drag", "scroll", "pointer-coordinate-space"} {
					if !slices.Contains(ops, name) {
						t.Errorf("fully authorized EIS missing %q in %v", name, ops)
					}
				}
				for _, name := range []string{"sync", "pointer-location"} {
					if slices.Contains(ops, name) {
						t.Errorf("unsupported %q advertised in %v", name, ops)
					}
				}
			}
		})
	}
}

func TestRemoteDesktopIncompleteEISPointerOrScrollAuthorizationRefusesCommands(t *testing.T) {
	for _, tc := range []struct {
		name      string
		disable   func(*eiSender)
		operation func(*RemoteDesktopBackend) error
	}{
		{"missing pointer object metadata", func(s *eiSender) { s.pointers[0].pointer = 0 }, func(b *RemoteDesktopBackend) error {
			_, err := b.PointerCoordinateSpace(context.Background())
			return err
		}},
		{"missing pointer device metadata", func(s *eiSender) { s.pointers[0].device = 0 }, func(b *RemoteDesktopBackend) error {
			_, err := b.PointerCoordinateSpace(context.Background())
			return err
		}},
		{"missing scroll device", func(s *eiSender) { s.scrollDev = 0 }, func(b *RemoteDesktopBackend) error { return b.ScrollDown(context.Background(), 3) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _, _ := newSyntheticRevocableRemoteDesktop(t)
			b.sender.pointers = []eiPointerDevice{{device: 501, pointer: 502, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}}}
			tc.disable(b.sender)
			if err := tc.operation(b); !errors.Is(err, ErrNotSupported) {
				t.Fatalf("missing EIS authorization returned %v, want typed unsupported error", err)
			}
			if err := b.stateError(); err != nil {
				t.Fatalf("missing capability revoked otherwise healthy session: %v", err)
			}
		})
	}
}

func TestRemoteDesktopDiagnosticsDoNotClaimMissingAuthorization(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	b.sender.pointers = []eiPointerDevice{{device: 501, pointer: 502, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}}}
	b.sender.keyboard = 0
	b.sender.pointers[0].pointer = 0
	details := strings.Join(b.Diagnostics(), " ")
	if strings.Contains(details, "granted keyboard and pointer") || !strings.Contains(details, "keyboard: unavailable") || !strings.Contains(details, "pointer: unavailable") {
		t.Fatalf("portal diagnostics claim absent EIS authorization: %s", details)
	}
}

func TestRemoteDesktopDiagnosticsDoNotAdvertiseClosedOrRevokedPortal(t *testing.T) {
	for _, tc := range []struct {
		name      string
		terminate func(*RemoteDesktopBackend, *portalSession) error
	}{
		{"closed", func(b *RemoteDesktopBackend, _ *portalSession) error { return b.Close() }},
		{"revoked", func(b *RemoteDesktopBackend, p *portalSession) error {
			p.signals <- &dbus.Signal{Name: portalSessionInterface + ".Closed", Path: p.path}
			select {
			case <-b.revoked:
				return nil
			case <-time.After(time.Second):
				return errors.New("revocation watcher did not close portal")
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, p, _ := newSyntheticRevocableRemoteDesktop(t)
			if err := tc.terminate(b, p); err != nil {
				t.Fatal(err)
			}
			details := strings.Join(b.Diagnostics(), " ")
			if strings.Contains(details, "session established") || !strings.Contains(details, "unavailable") {
				t.Fatalf("inactive portal still claims active device authorization: %q", details)
			}
			if len(b.SupportedOperations()) != 0 {
				t.Fatalf("inactive portal advertised operations: %v", b.SupportedOperations())
			}
		})
	}
}
