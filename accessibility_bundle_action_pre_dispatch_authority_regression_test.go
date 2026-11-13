package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

// Selecting an action is itself a mutation. A provider that ignores the
// cancellation context must never receive a pre-canceled action request.
func TestAccessibilityActionReceiptsRejectPreCanceledDispatch(t *testing.T) {
	for _, name := range []string{"by-name", "default"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			backend := &closingActionReceiptBackend{accessibilityAutomationFake: &accessibilityAutomationFake{
				bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}, accessibilityAutomationSpy: &accessibilityAutomationSpy{},
			}}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
			t.Cleanup(func() { _ = session.Close() })
			previous := session.Accessibility.backend
			session.Accessibility.backend = backend
			t.Cleanup(func() { session.Accessibility.backend = previous })
			id := accessibility.NodeID{BusName: "org.synthetic", ObjectPath: "/button", Generation: 1}
			var receipt accessibility.Action
			var err error
			if name == "by-name" {
				receipt, err = session.Accessibility.InvokeActionByName(ctx, id, "press")
			} else {
				receipt, err = session.Accessibility.InvokeDefaultAction(ctx, id)
			}
			if !errors.Is(err, context.Canceled) || backend.calls != 0 || receipt.Name != "" {
				t.Fatalf("pre-canceled %s mutation dispatched: receipt=%+v err=%v calls=%d", name, receipt, err, backend.calls)
			}
		})
	}
}
