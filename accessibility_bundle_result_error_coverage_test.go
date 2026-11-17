package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

type accessibilityBundleContractProbe struct {
	*accessibilityAutomationFake
	calls       int
	snapshotErr error
}

func newAccessibilityBundleContractProbe() *accessibilityBundleContractProbe {
	return &accessibilityBundleContractProbe{
		accessibilityAutomationFake: &accessibilityAutomationFake{
			bundleAccessibilityFake:    &bundleAccessibilityFake{gen: 1},
			accessibilityAutomationSpy: &accessibilityAutomationSpy{},
		},
	}
}

func (b *accessibilityBundleContractProbe) record() {
	b.calls++
}

func (b *accessibilityBundleContractProbe) Snapshot(ctx context.Context, root accessibility.NodeID, opts accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	b.record()
	if b.snapshotErr != nil {
		return accessibility.Snapshot{}, b.snapshotErr
	}
	return b.bundleAccessibilityFake.Snapshot(ctx, root, opts)
}

type directOutlineCancellationProbe struct {
	*accessibilityBundleContractProbe
	cancel context.CancelFunc
}

func (b *directOutlineCancellationProbe) Outline(context.Context, accessibility.NodeID, accessibility.SnapshotOptions, accessibility.OutlineOptions) (accessibility.Outline, error) {
	b.record()
	b.cancel()
	return accessibility.Outline{Root: accessibility.OutlineNode{Name: "private outline"}, Warnings: []string{"private warning"}}, nil
}

func TestAccessibilityDirectOutlineDiscardsResultAfterCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := &directOutlineCancellationProbe{accessibilityBundleContractProbe: newAccessibilityBundleContractProbe(), cancel: cancel}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, probe)
	defer session.Close()
	owner := session.Accessibility.backend
	session.Accessibility.backend = probe
	defer func() { session.Accessibility.backend = owner }()

	outline, err := session.Accessibility.Outline(ctx, accessibility.NodeID{}, accessibility.SnapshotOptions{}, accessibility.OutlineOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Outline error = %v, want context cancellation", err)
	}
	if outline.Root.Name != "" || len(outline.Warnings) != 0 {
		t.Fatalf("late-canceled outline exposed provider result: %+v", outline)
	}
	if probe.calls != 1 {
		t.Fatalf("direct outline provider calls = %d, want one", probe.calls)
	}
}

func TestAccessibilityFindOperationsPreserveSnapshotProviderFailure(t *testing.T) {
	providerErr := errors.New("snapshot transport closed")
	root := accessibility.NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/app", Generation: 1}
	for _, tc := range []struct {
		name string
		call func(*AccessibilityBundle) error
	}{
		{"find", func(b *AccessibilityBundle) error {
			_, err := b.Find(context.Background(), root, accessibility.Query{Name: "Save"}, accessibility.SnapshotOptions{})
			return err
		}},
		{"find one", func(b *AccessibilityBundle) error {
			_, err := b.FindOneSelector(context.Background(), root, accessibility.Selector{Name: "Save"}, accessibility.SnapshotOptions{})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := newAccessibilityBundleContractProbe()
			backend.snapshotErr = providerErr
			session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
			defer session.Close()

			if err := tc.call(session.Accessibility); !errors.Is(err, providerErr) {
				t.Fatalf("find error = %v, want snapshot provider failure", err)
			}
			if backend.calls != 1 {
				t.Fatalf("snapshot provider calls = %d, want one", backend.calls)
			}
		})
	}
}

func TestAccessibilityWindowRootWrapsMissingManagedWindowCapability(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()

	_, err := session.Accessibility.WindowRoot(context.Background(), "window-1")
	var operationErr *OperationError
	if !errors.Is(err, ErrUnavailable) || !errors.As(err, &operationErr) || operationErr.Operation != "window-root" {
		t.Fatalf("WindowRoot without window backend error = %v, want unavailable window-root capability", err)
	}
}

func TestAccessibilitySelectorSemanticActionReturnsExactDispatchReceipt(t *testing.T) {
	spy := &accessibilityAutomationSpy{exactAction: accessibility.Action{Index: 7, Name: "press"}}
	session, root := semanticActionCoverageFixture(spy)
	defer session.Close()

	receipt, err := session.Accessibility.InvokeSelectorSemanticAction(
		context.Background(), root, accessibility.Selector{Name: "Save", Role: "button"}, "press", accessibility.SnapshotOptions{},
	)
	if err != nil {
		t.Fatalf("InvokeSelectorSemanticAction: %v", err)
	}
	if receipt.Node.Name != "Save" || receipt.Action != spy.exactAction || receipt.Dispatch != accessibility.DispatchAccepted || receipt.Generation != root.Generation {
		t.Fatalf("selector action receipt lost the selected node/action/dispatch: %+v", receipt)
	}
	if spy.invokeCount != 1 {
		t.Fatalf("provider action calls = %d, want one", spy.invokeCount)
	}
}

type lateCanceledDefaultActionProbe struct {
	*accessibilityBundleContractProbe
	cancel  context.CancelFunc
	failure error
}

func (b *lateCanceledDefaultActionProbe) InvokeDefaultAction(context.Context, accessibility.NodeID) (accessibility.Action, error) {
	b.record()
	b.cancel()
	return accessibility.Action{Name: "private action"}, b.failure
}

func TestAccessibilityDefaultActionDiscardsLateResultAfterCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	providerErr := errors.New("provider completed after cancellation")
	probe := &lateCanceledDefaultActionProbe{accessibilityBundleContractProbe: newAccessibilityBundleContractProbe(), cancel: cancel, failure: providerErr}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, probe)
	defer session.Close()
	owner := session.Accessibility.backend
	session.Accessibility.backend = probe
	defer func() { session.Accessibility.backend = owner }()

	action, err := session.Accessibility.InvokeDefaultAction(ctx, accessibility.NodeID{})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, providerErr) {
		t.Fatalf("InvokeDefaultAction error = %v, want caller cancellation and provider cause", err)
	}
	if action != (accessibility.Action{}) {
		t.Fatalf("late-canceled default action exposed provider result: %+v", action)
	}
	if probe.calls != 1 {
		t.Fatalf("default action provider calls = %d, want one", probe.calls)
	}
}
