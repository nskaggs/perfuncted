package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

// A core accessibility backend cannot authorize mutations by advertising a
// capability name alone; optional automation must be implemented explicitly.
func TestAccessibilityOwnerRejectsAutomationWhenBackendDoesNotImplementIt(t *testing.T) {
	backend := newOwnerLifecycleBackend(1, "automation", "applications")
	owner := newAccessibilityBackendOwner(nil)
	owner.install(&coreOnlyAccessibilityBackend{Backend: backend})
	defer owner.Close()
	ctx := context.Background()
	id := accessibility.NodeID{BusName: "org.example.NotOwned"}
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"invoke action", func() error { return owner.InvokeAction(ctx, id, 0) }},
		{"invoke action by name", func() error { _, err := owner.InvokeActionByName(ctx, id, "activate"); return err }},
		{"default action", func() error { _, err := owner.InvokeDefaultAction(ctx, id); return err }},
		{"grab focus", func() error { return owner.GrabFocus(ctx, id) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, accessibility.ErrUnsupported) {
				t.Fatalf("automation without optional interface returned %v; want unsupported", err)
			}
		})
	}
	if _, err := owner.Applications(ctx); err != nil {
		t.Fatalf("core provider stopped working after rejected automation: %v", err)
	}
}
