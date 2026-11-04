package accessibility

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

// Fresh application discovery is a strict identity read, not a best-effort
// display list. A truncated desktop can hide a second matching application.
func TestPrivateATSPIApplicationFreshRefusesOversizedDesktopEnumeration(t *testing.T) {
	backend, _, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, _ *syntheticATSPINode) {
		original := root.children[0]
		root.children = make([]objectRef, maxApplications+1)
		for i := range root.children {
			root.children[i] = original
		}
	})
	app, err := backend.FindApplicationFresh(context.Background(), ApplicationFilter{Name: "Editor"})
	if !errors.Is(err, ErrIncompleteSnapshot) || app.ID.valid() || app.Name != "" {
		t.Fatalf("partial desktop disclosed a matching app: %+v %v", app, err)
	}
}

// An unowned or vanished desktop child cannot satisfy a requested PID; the
// system bus's identity lookup must succeed rather than being assumed.
func TestPrivateATSPIApplicationFreshDoesNotIgnoreUnverifiedPID(t *testing.T) {
	backend, _, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, _ *syntheticATSPINode) {
		root.children = []objectRef{{BusName: ":1.999999", ObjectPath: dbus.ObjectPath("/org/a11y/atspi/accessible/ghost")}}
	})
	app, err := backend.FindApplicationFresh(context.Background(), ApplicationFilter{PID: 424242})
	if err == nil || !strings.Contains(err.Error(), "resolve application PID") || app.ID.valid() {
		t.Fatalf("unverified PID became an application identity: %+v %v", app, err)
	}
}

func TestPrivateATSPIApplicationFreshRejectsNilContextWithoutPublishingIdentity(t *testing.T) {
	backend, _, _ := newSyntheticATSPIBackend(t, false)
	var ctx context.Context
	app, err := backend.FindApplicationFresh(ctx, ApplicationFilter{Name: "Editor"})
	if err == nil || !strings.Contains(err.Error(), "nil context") || app.ID.valid() {
		t.Fatalf("nil context returned a live application: %+v %v", app, err)
	}
}

// A child whose property service has vanished cannot be silently skipped as
// a non-match. Doing so would turn an incomplete enumeration into certainty.
func TestPrivateATSPIApplicationFreshRequiresAuthoritativeNameRead(t *testing.T) {
	backend, _, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, _ *syntheticATSPINode) {
		root.children = []objectRef{{BusName: ":1.999999", ObjectPath: dbus.ObjectPath("/org/a11y/atspi/accessible/ghost")}}
	})
	app, err := backend.FindApplicationFresh(context.Background(), ApplicationFilter{Name: "Editor"})
	if err == nil || !strings.Contains(err.Error(), "read application name") || app.ID.valid() {
		t.Fatalf("unreadable name treated as a known non-match: %+v %v", app, err)
	}
}
