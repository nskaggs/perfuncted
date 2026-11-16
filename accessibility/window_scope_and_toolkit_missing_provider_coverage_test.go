package accessibility

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

// Process ownership must be verified before fresh window correlation. A
// desktop child on an absent service is not an application with the requested
// PID, even if the reference itself has a syntactically valid object path.
func TestFreshWindowScopeRejectsUnavailableProcessOwner(t *testing.T) {
	backend, _, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, _ *syntheticATSPINode) {
		root.children = []objectRef{{BusName: ":1.999999", ObjectPath: dbus.ObjectPath("/org/a11y/atspi/accessible/vanished")}}
	})
	apps, err := backend.windowApplicationsFresh(context.Background(), WindowTarget{ID: "managed-window", PID: 48123})
	if err == nil || len(apps) != 0 || !strings.Contains(err.Error(), "resolve window application PID") {
		t.Fatalf("unverified process owner admitted: apps=%+v err=%v", apps, err)
	}
}

// A frame can disappear between discovery and GetApplication. An inaccessible
// provider method must not populate the toolkit cache with an invented name.
func TestToolkitForVanishedNodeDoesNotCacheProviderIdentity(t *testing.T) {
	backend, id, _ := newSyntheticATSPIBackend(t, false)
	vanished := backend.refID(objectRef{BusName: id.BusName, ObjectPath: dbus.ObjectPath("/org/a11y/atspi/accessible/vanished")})
	name, err := backend.toolkitForNode(context.Background(), vanished)
	if err == nil || name != "" || !strings.Contains(err.Error(), "get application") || errors.Is(err, ErrStaleNode) {
		t.Fatalf("vanished provider accepted as toolkit: name=%q err=%v", name, err)
	}
	backend.mu.RLock()
	cached := len(backend.toolkits)
	backend.mu.RUnlock()
	if cached != 0 {
		t.Fatalf("vanished application cached toolkit identity: %d", cached)
	}
}
